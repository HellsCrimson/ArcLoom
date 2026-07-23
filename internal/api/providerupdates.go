package api

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	goversion "github.com/hashicorp/go-version"

	"github.com/HellsCrimson/ArcLoom/internal/registry"
)

// providerUpdateDTO reports, per provider source, whether a newer version is
// available. "Current" is the version installed in the workspace lock file when
// known, otherwise an exact pin; "Latest" is the highest stable version the
// registry publishes. Update is true only when both are known and Latest is
// newer, so the UI can offer a one-click bump of the pin.
type providerUpdateDTO struct {
	Key     string `json:"key"`
	Source  string `json:"source"`
	Pinned  string `json:"pinned,omitempty"`  // the version constraint on the instance
	Current string `json:"current,omitempty"` // installed (lock) or exact pin
	Latest  string `json:"latest,omitempty"`  // highest stable available
	Update  bool   `json:"update"`
	Error   string `json:"error,omitempty"` // registry unreachable, etc.
}

// handleProviderUpdates checks each of a project's provider sources against the
// registry and reports available upgrades. It is best-effort: a source whose
// registry lookup fails is returned with an Error and Update=false, so an
// offline server still answers (just without upgrade hints).
func (s *Server) handleProviderUpdates(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.store.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}

	engine := string(p.Engine)
	if engine == "" {
		engine = s.cfg.DefaultEngine
	}

	// The lock file records the concrete versions last installed — the best
	// "current" reference. Absent (never initialised) it stays empty.
	installed := registry.InstalledVersions(filepath.Join(s.runDir(id, ""), ".terraform.lock.hcl"))

	// Collapse instances to unique sources (required_providers is per source /
	// local name; aliases share a version). Keep the strongest pin seen.
	type prov struct {
		key, source, pinned string
	}
	seen := map[string]int{}
	var provs []prov
	for _, inst := range p.Providers {
		if inst.Source == "" {
			continue
		}
		if idx, ok := seen[inst.Source]; ok {
			if provs[idx].pinned == "" && inst.Version != "" {
				provs[idx].pinned = inst.Version
			}
			continue
		}
		seen[inst.Source] = len(provs)
		provs = append(provs, prov{key: inst.Key, source: inst.Source, pinned: inst.Version})
	}

	out := make([]providerUpdateDTO, len(provs))
	var wg sync.WaitGroup
	for i, pv := range provs {
		wg.Add(1)
		go func(i int, pv prov) {
			defer wg.Done()
			dto := providerUpdateDTO{Key: pv.key, Source: pv.source, Pinned: pv.pinned}
			dto.Current = registry.InstalledFor(installed, pv.source)
			if dto.Current == "" {
				// No lock version: fall back to the pin, but only if it is an
				// exact version (a constraint range has no single "current").
				if _, err := goversion.NewVersion(strings.TrimSpace(pv.pinned)); err == nil {
					dto.Current = strings.TrimSpace(pv.pinned)
				}
			}

			versions, err := s.registry.AvailableVersions(r.Context(), engine, pv.source)
			if err != nil {
				dto.Error = err.Error()
				out[i] = dto
				return
			}
			dto.Latest = registry.LatestStable(versions)
			dto.Update = isNewer(dto.Latest, dto.Current)
			out[i] = dto
		}(i, pv)
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	writeJSON(w, http.StatusOK, out)
}

// isNewer reports whether latest is a strictly greater version than current.
// It returns false unless both parse as versions.
func isNewer(latest, current string) bool {
	if latest == "" || current == "" {
		return false
	}
	lv, err := goversion.NewVersion(latest)
	if err != nil {
		return false
	}
	cv, err := goversion.NewVersion(current)
	if err != nil {
		return false
	}
	return lv.GreaterThan(cv)
}
