package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// fakeKV is a stand-in Vault: the endpoints Check and the KV v2 data calls need.
func fakeKV(t *testing.T, token string) *httptest.Server {
	t.Helper()
	docs := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != token {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []string{"permission denied"}})
			return
		}
		switch {
		case r.URL.Path == "/v1/auth/token/lookup-self":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		case strings.HasPrefix(r.URL.Path, "/v1/secret/metadata/"):
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []string{}})
		case strings.HasPrefix(r.URL.Path, "/v1/secret/data/"):
			key := strings.TrimPrefix(r.URL.Path, "/v1/secret/data/")
			if r.Method == http.MethodPost {
				var body struct {
					Data map[string]any `json:"data"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				docs[key] = body.Data
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1}})
				return
			}
			doc, ok := docs[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []string{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"data": doc, "metadata": map[string]any{"version": 1},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// secretStoreServer builds an auth-disabled server (so admin routes are open)
// with a real secrets store and settings.
func secretStoreServer(t *testing.T, masterKey string) (*Server, http.Handler, *secrets.Store) {
	t.Helper()
	// The settings store needs a key of its own to encrypt the Vault token at rest.
	set, err := settings.NewStore(t.TempDir()+"/settings.yaml", masterKey)
	if err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewStore(t.TempDir(), masterKey)
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	srv := New(cfg, nil, store.NewMemory(), nil, sec, nil, authn, session.NewMemory(), user.NewMemory(), set, nil, nil, nil, nil)
	return srv, srv.Handler(), sec
}

func putSecretStore(t *testing.T, h http.Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings/secret-store", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecretStoreDefaultsToLocal(t *testing.T) {
	_, h, _ := secretStoreServer(t, "master")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/settings/secret-store", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var dto secretStoreDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Kind != settings.SecretStoreLocal || dto.Active != "local" || !dto.LocalEnabled {
		t.Fatalf("unexpected default: %+v", dto)
	}
}

func TestSecretStoreSwitchToVaultAndBack(t *testing.T) {
	_, h, sec := secretStoreServer(t, "master")
	kv := fakeKV(t, "tok")

	// Switching to the external store probes it, persists, and hot-swaps.
	rec := putSecretStore(t, h, map[string]any{"kind": "vault", "address": kv.URL, "token": "tok"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if sec.BackendName() != "vault" {
		t.Fatalf("store did not switch, got %q", sec.BackendName())
	}
	// And a secret written now lands in the external store.
	if err := sec.Set("p1", "token", "v"); err != nil {
		t.Fatalf("set after switch: %v", err)
	}

	// Switching back to local returns to the encrypted files on this host.
	rec = putSecretStore(t, h, map[string]any{"kind": "local"})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch back status = %d: %s", rec.Code, rec.Body)
	}
	if sec.BackendName() != "local" {
		t.Fatalf("expected local, got %q", sec.BackendName())
	}
}

func TestSecretStoreRejectsUnreachableVault(t *testing.T) {
	_, h, sec := secretStoreServer(t, "master")

	// Wrong token → the probe fails and NOTHING is persisted, so the instance
	// keeps reading the secrets it can actually reach.
	kv := fakeKV(t, "right")
	rec := putSecretStore(t, h, map[string]any{"kind": "vault", "address": kv.URL, "token": "wrong"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if sec.BackendName() != "local" {
		t.Fatalf("store should be untouched, got %q", sec.BackendName())
	}

	// A malformed address is rejected before any request.
	rec = putSecretStore(t, h, map[string]any{"kind": "vault", "address": "nonsense", "token": "t"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad address status = %d: %s", rec.Code, rec.Body)
	}
	// An unknown kind is a 400, not a silent local fallback.
	if rec := putSecretStore(t, h, map[string]any{"kind": "consul"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind status = %d", rec.Code)
	}
}

func TestSecretStoreMigratesOnSwitch(t *testing.T) {
	srv, h, sec := secretStoreServer(t, "master")

	p := &model.Project{ID: "proj-1", Name: "demo", Engine: model.EngineTofu}
	if err := srv.store.Create(p); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set(p.ID, "db_password", "local-value"); err != nil {
		t.Fatal(err)
	}

	kv := fakeKV(t, "tok")
	rec := putSecretStore(t, h, map[string]any{
		"kind": "vault", "address": kv.URL, "token": "tok", "migrate": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Migrated int    `json:"migrated"`
		Active   string `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Migrated != 1 || resp.Active != "vault" {
		t.Fatalf("migration response = %+v", resp)
	}
	// The value resolves from the NEW store, which is the whole point: a project
	// referencing it keeps planning.
	v, found, err := sec.Resolve(p.ID, "db_password")
	if err != nil || !found || v != "local-value" {
		t.Fatalf("resolve after migration = %q %v %v", v, found, err)
	}
}

func TestSecretStoreTestEndpoint(t *testing.T) {
	srv, h, _ := secretStoreServer(t, "master")
	kv := fakeKV(t, "tok")

	post := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/secret-store/test", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := post(map[string]any{"address": kv.URL, "token": "tok"}); rec.Code != http.StatusOK {
		t.Fatalf("healthy probe = %d: %s", rec.Code, rec.Body)
	}
	// A store that refuses us is 502 (the request was fine, the upstream said no).
	if rec := post(map[string]any{"address": kv.URL, "token": "nope"}); rec.Code != http.StatusBadGateway {
		t.Fatalf("bad token probe = %d: %s", rec.Code, rec.Body)
	}
	// The probe must not persist anything.
	if s := srv.settings.SecretStore(); s.Kind == settings.SecretStoreVault {
		t.Fatal("the test endpoint must not save the configuration")
	}
}

func TestSecretStoreTokenNeverReturned(t *testing.T) {
	_, h, _ := secretStoreServer(t, "master")
	kv := fakeKV(t, "super-secret-token")

	if rec := putSecretStore(t, h, map[string]any{
		"kind": "vault", "address": kv.URL, "token": "super-secret-token",
	}); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/settings/secret-store", nil))
	if strings.Contains(rec.Body.String(), "super-secret-token") {
		t.Fatalf("the token must never be returned: %s", rec.Body)
	}
	var dto secretStoreDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &dto)
	if !dto.TokenSet {
		t.Error("tokenSet should report that a token is on file")
	}

	// A save that omits the token keeps the stored one (the UI never has it to
	// resend), so an unrelated edit cannot break the connection.
	if rec := putSecretStore(t, h, map[string]any{
		"kind": "vault", "address": kv.URL, "prefix": "moved",
	}); rec.Code != http.StatusOK {
		t.Fatalf("re-save without token = %d: %s", rec.Code, rec.Body)
	}
}

func TestSecretStoreTokenFromEnvNeedsNoMasterKey(t *testing.T) {
	// An operator who does not want ArcLoom holding any credential at rest can
	// keep the token in the environment: no ARCLOOM_SECRET_KEY is then required.
	kv := fakeKV(t, "env-token")
	t.Setenv(vaultTokenEnv, "env-token")

	_, h, sec := secretStoreServer(t, "") // no master key anywhere

	rec := putSecretStore(t, h, map[string]any{"kind": "vault", "address": kv.URL})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if sec.BackendName() != "vault" {
		t.Fatalf("backend = %q", sec.BackendName())
	}
	// Secrets are writable through the external store even though the local
	// encrypted backend is disabled.
	if err := sec.Set("p1", "api_key", "v"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if v, found, err := sec.Resolve("p1", "api_key"); err != nil || !found || v != "v" {
		t.Fatalf("resolve = %q %v %v", v, found, err)
	}
}

func TestSecretStoreTokenNeedsMasterKeyToPersist(t *testing.T) {
	kv := fakeKV(t, "tok")
	_, h, _ := secretStoreServer(t, "") // no master key

	rec := putSecretStore(t, h, map[string]any{"kind": "vault", "address": kv.URL, "token": "tok"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	// The message must name what to do, not the unrelated OIDC secret.
	if body := rec.Body.String(); !strings.Contains(body, vaultTokenEnv) || !strings.Contains(body, "ARCLOOM_SECRET_KEY") {
		t.Fatalf("unhelpful error: %s", body)
	}
}
