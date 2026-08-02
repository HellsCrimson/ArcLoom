package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/store"
)

// maxMoved bounds the list. A refactor that folds hand-written resources into
// one for_each'd block produces one block per moved address, so this is
// generous for the real case and still stops a bad client growing the model.
const maxMoved = 500

// handleSetMoved replaces the project's `moved` blocks.
//
// Replace-the-whole-list, like handleSetImports and for the same reasons: the
// UI edits them as one table, and "clear the ones that have been applied" is
// then a single call.
//
// Both fields are validated as STATIC ADDRESSES (hclgen.ValidateAddress), not
// merely as expressions: the engine resolves a moved block before evaluating
// anything, so `x.y[each.key]` is refused there — better to say so here than to
// let it surface as a plan error. Validation matters extra because store
// persist swallows generation errors.
func (s *Server) handleSetMoved(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Moved []struct {
			ID   string `json:"id"`
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"moved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if len(req.Moved) > maxMoved {
		writeErr(w, http.StatusBadRequest, "too many moved blocks")
		return
	}

	moved := make([]model.Moved, 0, len(req.Moved))
	for i, in := range req.Moved {
		mv := model.Moved{
			ID:   strings.TrimSpace(in.ID),
			From: strings.TrimSpace(in.From),
			To:   strings.TrimSpace(in.To),
		}
		if mv.From == "" || mv.To == "" {
			writeErr(w, http.StatusBadRequest, "moved block: `from` and `to` are required")
			return
		}
		for label, addr := range map[string]string{"from": mv.From, "to": mv.To} {
			if err := hclgen.ValidateAddress(addr); err != nil {
				writeErr(w, http.StatusBadRequest, "moved block "+itoa(i+1)+": "+label+": "+err.Error())
				return
			}
		}
		// A block that moves an address onto itself is not a rename; the engine
		// rejects it ("cannot move to the same address"), so catch the typo here.
		if mv.From == mv.To {
			writeErr(w, http.StatusBadRequest, "moved block "+itoa(i+1)+": `from` and `to` are the same address")
			return
		}
		if mv.ID == "" {
			mv.ID = uuid.NewString()
		}
		moved = append(moved, mv)
	}

	err := s.store.Update(id, func(p *model.Project) error {
		p.Moved = moved
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
