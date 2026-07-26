package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/git"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// projectDir returns the on-disk folder for a project (matches the Disk store
// layout). Git operations act on this folder.
func (s *Server) projectDir(id string) string {
	return filepath.Join(s.cfg.DataDir, id)
}

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	st, err := git.GetStatus(s.projectDir(chi.URLParam(r, "id")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var req struct {
		Message string   `json:"message"`
		Name    string   `json:"name,omitempty"`
		Email   string   `json:"email,omitempty"`
		Paths   []string `json:"paths,omitempty"` // empty = stage everything
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	hash, err := git.CommitChanges(s.projectDir(id), req.Message, req.Name, req.Email, req.Paths)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hash": hash})
}

func (s *Server) handleGitLog(w http.ResponseWriter, r *http.Request) {
	commits, err := git.Log(s.projectDir(chi.URLParam(r, "id")), 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if commits == nil {
		commits = []git.Commit{}
	}
	writeJSON(w, http.StatusOK, commits)
}

func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	dir := s.projectDir(chi.URLParam(r, "id"))
	var diff string
	var err error
	if commit := r.URL.Query().Get("commit"); commit != "" {
		diff, err = git.CommitDiff(dir, commit)
	} else {
		diff, err = git.WorkingDiff(dir)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
}

// handleGitRestore rewrites the working tree to a commit and returns the
// reloaded project (the model is re-read from the restored arcloom.yaml).
func (s *Server) handleGitRestore(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Commit == "" {
		writeErr(w, http.StatusBadRequest, "commit is required")
		return
	}
	if err := git.Restore(s.projectDir(id), req.Commit); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.store.Get(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleGitBranches(w http.ResponseWriter, r *http.Request) {
	branches, err := git.Branches(s.projectDir(chi.URLParam(r, "id")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if branches == nil {
		branches = []git.Branch{}
	}
	writeJSON(w, http.StatusOK, branches)
}

func (s *Server) handleGitCreateBranch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := git.CreateBranch(s.projectDir(chi.URLParam(r, "id")), req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGitDeleteBranch(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := git.DeleteBranch(s.projectDir(chi.URLParam(r, "id")), name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGitCheckout switches branches and returns the reloaded project (the
// model is re-read from the target branch's arcloom.yaml).
func (s *Server) handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	s.gitReloadOp(w, r, git.SwitchBranch)
}

// handleGitUndoCommit drops the newest commit while keeping every file, so the
// user can re-commit it differently (message, split, extra changes). This is the
// only history-rewriting endpoint — everything else moves history forward — so
// undoing a commit origin already has requires an explicit force: without it the
// refusal comes back as 409 for the UI to escalate its confirmation.
//
// The working tree is untouched, so unlike restore/checkout/merge there is no
// model to reload; the response is the commit that was undone.
func (s *Server) handleGitUndoCommit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Force bool `json:"force"`
	}
	// An empty body is valid (force defaults to false).
	_ = json.NewDecoder(r.Body).Decode(&req)

	undone, err := git.UndoLastCommit(s.projectDir(id), req.Force)
	if errors.Is(err, git.ErrCommitPushed) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, undone)
}

// handleGitMerge fast-forward merges a branch into the current one and returns
// the reloaded project.
func (s *Server) handleGitMerge(w http.ResponseWriter, r *http.Request) {
	s.gitReloadOp(w, r, git.MergeBranch)
}

// gitReloadOp runs a git op that mutates the working tree by branch name, then
// returns the reloaded project so the UI re-renders the restored model.
func (s *Server) gitReloadOp(w http.ResponseWriter, r *http.Request, fn func(dir, name string) error) {
	id := chi.URLParam(r, "id")
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := fn(s.projectDir(id), req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.store.Get(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleGitRemote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeErr(w, http.StatusBadRequest, "url is required")
		return
	}
	if err := git.SetRemote(s.projectDir(chi.URLParam(r, "id")), req.URL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGitPush(w http.ResponseWriter, r *http.Request) {
	s.gitSync(w, r, git.Push)
}

func (s *Server) handleGitPull(w http.ResponseWriter, r *http.Request) {
	s.gitSync(w, r, git.Pull)
}

// gitSync handles push/pull, which share the same request shape. Credentials
// come from the request body (transient, used only for this request) when
// present; otherwise the project's stored credential is used, so a homelabber
// need not retype a PAT / SSH key every time.
func (s *Server) gitSync(w http.ResponseWriter, r *http.Request, fn func(dir string, a git.Auth) error) {
	id := chi.URLParam(r, "id")
	var req struct {
		Username string `json:"username,omitempty"`
		Token    string `json:"token,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	auth := git.Auth{Username: req.Username, Token: req.Token}
	// Fall back to (or supplement with) the stored credential.
	if s.settings != nil {
		if c, ok := s.settings.GitCreds(id); ok {
			if auth.Token == "" && auth.Username == "" {
				auth.Username = c.Username
			}
			if auth.Token == "" {
				auth.Token = c.Token
			}
			auth.SSHKey = c.SSHKey
			auth.Passphrase = c.Passphrase
		}
	}
	if err := fn(s.projectDir(id), auth); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGitCredentials returns credential metadata (never any secret).
func (s *Server) handleGitCredentials(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeJSON(w, http.StatusOK, settings.GitCredMeta{})
		return
	}
	writeJSON(w, http.StatusOK, s.settings.GitCredMeta(chi.URLParam(r, "id")))
}

// handleSetGitCredentials stores a project's remote credential (encrypted).
func (s *Server) handleSetGitCredentials(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var req struct {
		AuthType   string `json:"authType"`
		Username   string `json:"username,omitempty"`
		Token      string `json:"token,omitempty"`
		SSHKey     string `json:"sshKey,omitempty"`
		Passphrase string `json:"passphrase,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.AuthType != "https" && req.AuthType != "ssh" {
		writeErr(w, http.StatusBadRequest, "authType must be \"https\" or \"ssh\"")
		return
	}
	err := s.settings.SetGitCreds(id, settings.GitCred{
		AuthType:   req.AuthType,
		Username:   req.Username,
		Token:      req.Token,
		SSHKey:     req.SSHKey,
		Passphrase: req.Passphrase,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.settings.GitCredMeta(id))
}

// handleDeleteGitCredentials clears a project's stored credential.
func (s *Server) handleDeleteGitCredentials(w http.ResponseWriter, r *http.Request) {
	if s.settings != nil {
		if err := s.settings.DeleteGitCreds(chi.URLParam(r, "id")); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
