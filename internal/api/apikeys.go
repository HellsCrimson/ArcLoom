package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// apiKeyCtxKey keys the *apikey.Key that authenticated a request. Absent for a
// cookie session — which is what every "is this a key?" check keys off.
type apiKeyCtxKey struct{}

// keyFromContext returns the API key that authenticated the request, if any.
func keyFromContext(r *http.Request) (*apikey.Key, bool) {
	k, ok := r.Context().Value(apiKeyCtxKey{}).(*apikey.Key)
	return k, ok
}

// bearerToken extracts an `Authorization: Bearer <token>` value.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// userForKey resolves a presented token to its owning user. The owner must
// still be live and enabled: revoking or disabling an account must take every
// key they minted with it, without any key-side bookkeeping.
func (s *Server) userForKey(token string) (*user.User, *apikey.Key, bool) {
	if s.apikeys == nil {
		return nil, nil, false
	}
	key, err := s.apikeys.Verify(token)
	if err != nil {
		return nil, nil, false
	}
	u, err := s.users.Get(key.UserID)
	if err != nil || u.Disabled {
		return nil, nil, false
	}
	// Best-effort last-used stamp; never fail the request over it.
	go func(id string) { _ = s.apikeys.Touch(id, time.Now()) }(key.ID)
	return u, key, true
}

// keyDenies returns a human-readable refusal when the request's API key does
// not permit an action, or "" when it does (including when the request is not
// key-authenticated at all).
//
// perm may be "" for a read, which needs no scope but still honours a key's
// project restriction. projectID may be "" for a route with no {id}.
//
// This is the whole of the "a key restricts, never escalates" rule: it runs
// *after* the user's own permission and the project ACL have already passed, so
// it can only ever subtract.
func keyDenies(r *http.Request, perm, projectID string) string {
	key, ok := keyFromContext(r)
	if !ok {
		return ""
	}
	if perm != "" && !key.HasScope(perm) {
		return "this API key does not have the \"" + perm + "\" scope"
	}
	if projectID != "" && !key.AllowsProject(projectID) {
		return "this API key is not scoped to this project"
	}
	return ""
}

// effectiveUser returns what a credential can actually do: for a cookie session
// the user unchanged, for a key the intersection of the user's permissions with
// the key's scopes, with Admin cleared.
//
// It is a *view* for reporting (/auth/me, and the MCP tool filter), not the
// enforcement path — enforcement stays in the middleware, where a mistake fails
// closed. Both must agree, which is why they share this one definition of the
// intersection.
func effectiveUser(u *user.User, key *apikey.Key) *user.User {
	if u == nil || key == nil {
		return u
	}
	cp := *u
	cp.Admin = false
	perms := []string{}
	for _, p := range user.AllPermissions() {
		if u.Can(p) && key.HasScope(p) {
			perms = append(perms, p)
		}
	}
	cp.Permissions = perms
	return &cp
}

// --- HTTP surface ---

// apiKeyDTO is a key as reported to the UI. It never carries the secret or its
// hash: the secret exists in exactly one response, the create one.
type apiKeyDTO struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Scopes     []string `json:"scopes"`
	Projects   []string `json:"projects"`
	CreatedAt  string   `json:"createdAt"`
	ExpiresAt  string   `json:"expiresAt,omitempty"`
	LastUsedAt string   `json:"lastUsedAt,omitempty"`
	Disabled   bool     `json:"disabled,omitempty"`
	// Username is filled only for the admin listing, where keys from several
	// owners are shown together.
	Username string `json:"username,omitempty"`
}

func apiKeyToDTO(k *apikey.Key) apiKeyDTO {
	d := apiKeyDTO{
		ID:        k.ID,
		Label:     k.Label,
		Scopes:    k.Scopes,
		Projects:  k.Projects,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
		Disabled:  k.Disabled,
	}
	if d.Scopes == nil {
		d.Scopes = []string{}
	}
	if d.Projects == nil {
		d.Projects = []string{}
	}
	if k.ExpiresAt != nil {
		d.ExpiresAt = k.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if k.LastUsedAt != nil {
		d.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return d
}

// sortKeys orders newest first so the UI listing is stable (both stores return
// an unspecified order).
func sortKeys(keys []*apikey.Key) {
	sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt.After(keys[j].CreatedAt) })
}

type createAPIKeyRequest struct {
	Label     string   `json:"label"`
	Scopes    []string `json:"scopes"`
	Projects  []string `json:"projects"`
	ExpiresIn string   `json:"expiresIn"` // Go duration, e.g. "720h"; empty = never
}

// createAPIKeyResponse is the only place a secret is ever returned.
type createAPIKeyResponse struct {
	Key   apiKeyDTO `json:"key"`
	Token string    `json:"token"`
}

// requireKeyStore writes a 503 when API keys are not configured, so a caller
// gets a clear reason instead of a confusing 404/500.
func (s *Server) requireKeyStore(w http.ResponseWriter) bool {
	if s.apikeys == nil {
		writeErr(w, http.StatusServiceUnavailable, "API keys are not enabled on this server")
		return false
	}
	return true
}

// handleListAPIKeys returns the caller's own keys.
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !s.requireKeyStore(w) {
		return
	}
	u, _ := userFromContext(r)
	if u == nil {
		writeJSON(w, http.StatusOK, []apiKeyDTO{})
		return
	}
	keys, err := s.apikeys.ListByUser(u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list api keys: "+err.Error())
		return
	}
	sortKeys(keys)
	out := make([]apiKeyDTO, 0, len(keys))
	for _, k := range keys {
		out = append(out, apiKeyToDTO(k))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateAPIKey mints a key for the calling user.
//
// A key may only ever be *narrower* than its owner, so requested scopes are
// validated against what the owner actually holds rather than silently stored
// and refused later at every call — a key that looks like it grants deploy but
// 403s on every plan is worse than a 400 at creation.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireKeyStore(w) {
		return
	}
	u, _ := userFromContext(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		writeErr(w, http.StatusBadRequest, "label is required")
		return
	}
	if len(label) > 100 {
		writeErr(w, http.StatusBadRequest, "label is too long")
		return
	}
	scopes, err := normalizeScopes(req.Scopes, u)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	projects, err := normalizeKeyProjects(req.Projects)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	key := &apikey.Key{
		UserID:   u.ID,
		Label:    label,
		Scopes:   scopes,
		Projects: projects,
	}
	if d := strings.TrimSpace(req.ExpiresIn); d != "" {
		dur, err := time.ParseDuration(d)
		if err != nil || dur <= 0 {
			writeErr(w, http.StatusBadRequest, "expiresIn must be a positive duration like \"720h\"")
			return
		}
		exp := time.Now().Add(dur)
		key.ExpiresAt = &exp
	}
	token, err := s.apikeys.Create(key)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create api key: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, createAPIKeyResponse{Key: apiKeyToDTO(key), Token: token})
}

// handleDeleteAPIKey revokes one of the caller's keys. An admin may revoke any
// key; everyone else only their own — and the 404 for someone else's key is
// deliberate, so the endpoint does not confirm that an id exists.
func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireKeyStore(w) {
		return
	}
	id := chi.URLParam(r, "kid")
	key, err := s.apikeys.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "api key not found")
		return
	}
	if u, _ := userFromContext(r); u != nil && !u.Admin && key.UserID != u.ID {
		writeErr(w, http.StatusNotFound, "api key not found")
		return
	}
	if err := s.apikeys.Delete(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete api key: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListAllAPIKeys is the admin view: every key on the instance, with its
// owner's username, so a leaked or forgotten credential can be found and
// revoked by someone other than the person who made it.
func (s *Server) handleListAllAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !s.requireKeyStore(w) {
		return
	}
	keys, err := s.apikeys.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list api keys: "+err.Error())
		return
	}
	sortKeys(keys)
	out := make([]apiKeyDTO, 0, len(keys))
	for _, k := range keys {
		d := apiKeyToDTO(k)
		if owner, err := s.users.Get(k.UserID); err == nil {
			d.Username = owner.Username
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

// normalizeScopes validates requested scopes: each must be a real permission
// and one the owner actually holds. Duplicates are collapsed; order follows
// user.AllPermissions so stored keys are comparable.
func normalizeScopes(scopes []string, owner *user.User) ([]string, error) {
	out := []string{}
	for _, p := range user.AllPermissions() {
		if !slices.Contains(scopes, p) {
			continue
		}
		if owner != nil && !owner.Can(p) {
			return nil, errScope(p)
		}
		out = append(out, p)
	}
	for _, s := range scopes {
		if !slices.Contains(user.AllPermissions(), s) {
			return nil, errUnknownScope(s)
		}
	}
	return out, nil
}

func errScope(p string) error {
	return &httpError{"you cannot grant the \"" + p + "\" scope because you do not hold that permission"}
}

func errUnknownScope(s string) error {
	return &httpError{"unknown scope \"" + s + "\""}
}

// httpError is a plain message carrier for the validation helpers above.
type httpError struct{ msg string }

func (e *httpError) Error() string { return e.msg }

// normalizeKeyProjects validates the optional project restriction. Ids become
// path segments downstream, so they get the same shape check every other
// project id gets.
func normalizeKeyProjects(ids []string) ([]string, error) {
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !validID(id) {
			return nil, &httpError{"invalid project id \"" + id + "\""}
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// apiKeyCount reports how many keys exist, for the admin instance panel. A
// failure reads as zero rather than failing the panel.
func (s *Server) apiKeyCount() int {
	if s.apikeys == nil {
		return 0
	}
	keys, err := s.apikeys.List()
	if err != nil {
		return 0
	}
	return len(keys)
}
