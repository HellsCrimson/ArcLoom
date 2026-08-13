package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/store"
)

// maxImports bounds the list so a bad client cannot grow the model without
// limit; a real adoption run is a handful of blocks, each of which may itself
// cover hundreds of instances through for_each.
const maxImports = 200

// handleSetImports replaces the project's `import` blocks.
//
// Replace-the-whole-list rather than per-block CRUD: import blocks are
// transient (add, apply once, delete) and the UI edits them as one table, so a
// single PUT is both the simplest contract and the one that makes "remove them
// all now that they are adopted" a single call.
//
// Every field is a raw HCL expression validated here with hclgen.ValidateExpr —
// store persist swallows generation errors, so an unparseable `to` would
// otherwise be discovered only when the next plan failed to render.
func (s *Server) handleSetImports(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Imports []struct {
			ID       string `json:"id"`
			ForEach  string `json:"forEach"`
			To       string `json:"to"`
			ImportID string `json:"importId"`
			Provider string `json:"provider"`
		} `json:"imports"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if len(req.Imports) > maxImports {
		writeErr(w, http.StatusBadRequest, "too many import blocks")
		return
	}

	imports := make([]model.Import, 0, len(req.Imports))
	for i, in := range req.Imports {
		imp := model.Import{
			ID:       strings.TrimSpace(in.ID),
			ForEach:  strings.TrimSpace(in.ForEach),
			To:       strings.TrimSpace(in.To),
			ImportID: strings.TrimSpace(in.ImportID),
			Provider: strings.TrimSpace(in.Provider),
		}
		if imp.To == "" || imp.ImportID == "" {
			writeErr(w, http.StatusBadRequest, "import block: `to` and `id` are required")
			return
		}
		for label, expr := range map[string]string{"to": imp.To, "id": imp.ImportID, "for_each": imp.ForEach} {
			if expr == "" {
				continue
			}
			if err := hclgen.ValidateExpr(expr); err != nil {
				writeErr(w, http.StatusBadRequest, "import block "+itoa(i+1)+": "+label+": "+err.Error())
				return
			}
		}
		if imp.Provider != "" && !providerRefRe.MatchString(imp.Provider) {
			writeErr(w, http.StatusBadRequest, "invalid provider reference")
			return
		}
		if imp.ID == "" {
			imp.ID = uuid.NewString()
		}
		imports = append(imports, imp)
	}

	err := s.store.Update(id, func(p *model.Project) error {
		p.Imports = imports
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

// itoa renders a block's 1-based position for an error message. The hand-rolled
// version this replaced produced punctuation past 99 — reachable, since a
// refactor that folds hand-written resources into one for_each writes one moved
// block per address and maxMoved is 500.
func itoa(n int) string { return strconv.Itoa(n) }
