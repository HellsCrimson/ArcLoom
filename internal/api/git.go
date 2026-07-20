package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/git"
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
		Message string `json:"message"`
		Name    string `json:"name,omitempty"`
		Email   string `json:"email,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	hash, err := git.CommitChanges(s.projectDir(id), req.Message, req.Name, req.Email)
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

// gitSync handles push/pull, which share the same request shape. The token is
// transient (used for this request only, never persisted).
func (s *Server) gitSync(w http.ResponseWriter, r *http.Request, fn func(dir, username, token string) error) {
	var req struct {
		Username string `json:"username,omitempty"`
		Token    string `json:"token,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := fn(s.projectDir(chi.URLParam(r, "id")), req.Username, req.Token); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
