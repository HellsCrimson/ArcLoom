package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// usernameRe validates local usernames: start with a letter or underscore, then
// letters/digits/dot/underscore/hyphen.
var usernameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]*$`)

// adminUserDTO is a user as seen by the admin UI (never includes the hash).
type adminUserDTO struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email,omitempty"`
	Provider    string    `json:"provider"`
	Admin       bool      `json:"admin"`
	Permissions []string  `json:"permissions"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

func toAdminUserDTO(u *user.User) adminUserDTO {
	perms := u.Permissions
	if perms == nil {
		perms = []string{}
	}
	return adminUserDTO{
		ID: u.ID, Username: u.Username, Email: u.Email, Provider: u.Provider,
		Admin: u.Admin, Permissions: perms, Disabled: u.Disabled, CreatedAt: u.CreatedAt,
	}
}

// sanitizePermissions keeps only known, de-duplicated permission strings.
func sanitizePermissions(in []string) []string {
	out := []string{}
	for _, p := range user.AllPermissions() {
		if slices.Contains(in, p) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	slices.SortFunc(users, func(a, b *user.User) int {
		if a.Username < b.Username {
			return -1
		}
		return 1
	})
	out := make([]adminUserDTO, 0, len(users))
	for _, u := range users {
		out = append(out, toAdminUserDTO(u))
	}
	writeJSON(w, http.StatusOK, out)
}

type createUserRequest struct {
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	Email       string   `json:"email"`
	Admin       bool     `json:"admin"`
	Permissions []string `json:"permissions"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !usernameRe.MatchString(req.Username) {
		writeErr(w, http.StatusBadRequest, "invalid username (letters, digits, . _ -; must start with a letter or _)")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if _, err := s.users.ByUsername(req.Username); err == nil {
		writeErr(w, http.StatusConflict, "a user with that username already exists")
		return
	}
	// Seat limit: the community tier caps user count; more requires a license
	// granting the multi-user feature.
	if s.license.SeatsExceeded(s.userCount()) {
		writeErr(w, http.StatusForbidden, fmt.Sprintf("user limit reached (%d) — ArcLoom Enterprise is required for more users", s.license.SeatLimit()))
		return
	}
	hash, err := auth.Hash(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	u := &user.User{
		ID:           uuid.NewString(),
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		Provider:     auth.ProviderLocal,
		CreatedAt:    time.Now(),
		Admin:        req.Admin,
		Permissions:  sanitizePermissions(req.Permissions),
	}
	if err := s.users.Create(u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAdminUserDTO(u))
}

type updateUserRequest struct {
	Email       *string   `json:"email"`
	Admin       *bool     `json:"admin"`
	Permissions *[]string `json:"permissions"`
	Disabled    *bool     `json:"disabled"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "uid")
	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Guard against removing the last admin or self-lockout.
	if (req.Admin != nil && !*req.Admin) || (req.Disabled != nil && *req.Disabled) {
		demote := req.Admin != nil && !*req.Admin
		disable := req.Disabled != nil && *req.Disabled
		if err := s.guardLastAdmin(targetID, demote, disable); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	err := s.users.Update(targetID, func(u *user.User) error {
		if req.Email != nil {
			u.Email = *req.Email
		}
		if req.Admin != nil {
			u.Admin = *req.Admin
		}
		if req.Permissions != nil {
			u.Permissions = sanitizePermissions(*req.Permissions)
		}
		if req.Disabled != nil {
			u.Disabled = *req.Disabled
		}
		return nil
	})
	if errors.Is(err, user.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Revoking access takes effect immediately.
	if (req.Disabled != nil && *req.Disabled) || (req.Admin != nil && !*req.Admin) || req.Permissions != nil {
		_ = s.sessions.DeleteByUser(targetID)
	}
	u, _ := s.users.Get(targetID)
	writeJSON(w, http.StatusOK, toAdminUserDTO(u))
}

type resetPasswordRequest struct {
	NewPassword string `json:"newPassword"`
}

func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "uid")
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.NewPassword) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := auth.Hash(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	err = s.users.Update(targetID, func(u *user.User) error {
		u.PasswordHash = hash
		return nil
	})
	if errors.Is(err, user.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.sessions.DeleteByUser(targetID) // force re-login with the new password
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "uid")
	if err := s.guardLastAdmin(targetID, true, true); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.users.Delete(targetID); err != nil {
		if errors.Is(err, user.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.sessions.DeleteByUser(targetID)
	w.WriteHeader(http.StatusNoContent)
}

// guardLastAdmin rejects an operation that would leave the project with no
// active admin. demote/disable describe what would happen to targetID.
func (s *Server) guardLastAdmin(targetID string, demote, disable bool) error {
	target, err := s.users.Get(targetID)
	if err != nil {
		return nil // not found; the caller's Update/Delete will 404
	}
	if !target.Admin {
		return nil // removing a non-admin never affects admin count
	}
	all, err := s.users.List()
	if err != nil {
		return err
	}
	otherActiveAdmins := 0
	for _, u := range all {
		if u.ID != targetID && u.Admin && !u.Disabled {
			otherActiveAdmins++
		}
	}
	if otherActiveAdmins == 0 && (demote || disable) {
		return errors.New("cannot remove the last admin")
	}
	return nil
}
