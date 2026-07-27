package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/store"
)

// handleSetFile creates or replaces a companion file (upsert by path). These
// are the templates/scripts HCL reaches through file()/templatefile(); they are
// emitted next to the generated HCL, so they reach the run workspace with no
// runner change at all.
//
// The path is validated here rather than trusted: it becomes a filepath.Join
// argument in both the project directory and the run workspace.
func (s *Server) handleSetFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Path string `json:"path"`
		// OldPath renames: the file stored under it is removed in the same
		// update, so a rename can't briefly leave two copies (and can't be
		// half-applied if the write fails).
		OldPath string `json:"oldPath,omitempty"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	clean, err := model.ValidateFilePath(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Content) > model.MaxFileSize {
		writeErr(w, http.StatusBadRequest, "file is too large (max 256 KiB) — companion files are templates and scripts, not assets")
		return
	}
	oldClean := ""
	if req.OldPath != "" {
		if oldClean, err = model.ValidateFilePath(req.OldPath); err != nil {
			writeErr(w, http.StatusBadRequest, "oldPath: "+err.Error())
			return
		}
	}

	err = s.store.Update(id, func(p *model.Project) error {
		if oldClean != "" && oldClean != clean {
			p.Files = removeFile(p.Files, oldClean)
		}
		for i := range p.Files {
			if p.Files[i].Path == clean {
				p.Files[i].Content = req.Content
				return nil
			}
		}
		if len(p.Files) >= model.MaxFilesPerScope {
			return errTooManyFiles
		}
		p.Files = append(p.Files, model.ProjectFile{Path: clean, Content: req.Content})
		return nil
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "project not found")
		return
	case errors.Is(err, errTooManyFiles):
		writeErr(w, http.StatusBadRequest, "too many files in this project (max 100)")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

var errTooManyFiles = errors.New("too many files")

// handleDeleteFile removes a companion file by ?path=.
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	clean, err := model.ValidateFilePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err = s.store.Update(id, func(p *model.Project) error {
		p.Files = removeFile(p.Files, clean)
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

func removeFile(files []model.ProjectFile, path string) []model.ProjectFile {
	out := files[:0]
	for _, f := range files {
		if f.Path != path {
			out = append(out, f)
		}
	}
	return out
}
