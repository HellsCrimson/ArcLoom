package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/license"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// handleGetCost returns a project's cost-estimation config (any authenticated
// user; the config is not secret).
func (s *Server) handleGetCost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var cfg settings.Cost
	if s.settings != nil {
		cfg = s.settings.Cost(id)
	}
	if cfg.Overrides == nil {
		cfg.Overrides = []settings.CostOverride{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg})
}

// handleSetCost saves a project's cost-estimation config. Enabling it (or
// storing overrides) is an enterprise feature; disabling / clearing is always
// allowed so an expired license can still turn it off.
func (s *Server) handleSetCost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if s.settings == nil {
		writeErr(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	var req settings.Cost
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if (req.Enabled || len(req.Overrides) > 0) && !s.license.Has(license.FeatureCost) {
		writeErr(w, http.StatusForbidden, "cost estimation requires an ArcLoom Enterprise license")
		return
	}
	// Normalize: drop blank-target overrides so the UI's empty rows don't persist.
	cleaned := req.Overrides[:0]
	for _, o := range req.Overrides {
		o.Target = strings.TrimSpace(o.Target)
		if o.Target == "" {
			continue
		}
		cleaned = append(cleaned, o)
	}
	req.Overrides = cleaned
	if err := s.settings.SetCost(id, req); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Overrides == nil {
		req.Overrides = []settings.CostOverride{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": req})
}

// attachCost computes the cost breakdown for a completed plan and attaches it to
// res.Cost, when the cost feature is licensed and enabled for the project. It is
// best-effort: an Infracost failure degrades to overrides-only and is reported
// through the streamed log. Called while <dir>/tfplan still exists (right after
// run.Plan), so Infracost can read the saved plan.
func (s *Server) attachCost(ctx context.Context, run runner.Runner, dir string, p *model.Project, res *runner.PlanResult, logW *lineWriter) {
	if s.settings == nil || !s.license.Has(license.FeatureCost) {
		return
	}
	cfg := s.settings.Cost(p.ID)
	if !cfg.Enabled {
		return
	}
	prices, err := run.Infracost(ctx, dir)
	cost := buildCost(res.Changes, cfg.Overrides, prices, err)
	res.Cost = cost
	if cost.Error != "" {
		logW.emit("cost: infracost unavailable (" + cost.Error + "); using price overrides only")
	}
}

// buildCost merges user-defined price overrides with Infracost pricing into a
// per-resource monthly breakdown. Overrides always win over Infracost, and an
// address-targeted override wins over a type-targeted one. infracostErr is the
// error (if any) from the Infracost lookup; the breakdown is still built from
// overrides in that case, with the error recorded on the result.
func buildCost(changes []runner.PlanChange, overrides []settings.CostOverride, infracost map[string]float64, infracostErr error) *runner.CostResult {
	res := &runner.CostResult{Currency: "USD", Lines: []runner.CostLine{}}
	if infracostErr != nil {
		msg, _, _ := strings.Cut(infracostErr.Error(), "\n")
		res.Error = msg
	} else if infracost != nil {
		res.InfracostUsed = true
	}

	// Universe of managed resources to price: those changing in this plan (not
	// deletes) plus any Infracost priced (covers no-op resources when Infracost
	// is available). Keyed by address; type derived from the change or address.
	types := map[string]string{}
	order := []string{}
	seen := func(addr, typ string) {
		if _, ok := types[addr]; !ok {
			order = append(order, addr)
		}
		if typ != "" {
			types[addr] = typ
		} else if _, ok := types[addr]; !ok {
			types[addr] = typeFromAddress(addr)
		}
	}
	for _, c := range changes {
		if c.Mode == "data" || c.Action == "delete" {
			continue
		}
		seen(c.Address, c.Type)
	}
	for addr := range infracost {
		seen(addr, "")
	}

	for _, addr := range order {
		typ := types[addr]
		monthly, source := 0.0, ""
		if v, ok := matchOverride(addr, typ, overrides); ok {
			monthly, source = v, "override"
		} else if v, ok := infracost[addr]; ok {
			monthly, source = v, "infracost"
		}
		if source == "" {
			continue // no price known for this resource
		}
		res.Lines = append(res.Lines, runner.CostLine{Address: addr, Type: typ, Monthly: monthly, Source: source})
		res.TotalMonthly += monthly
	}
	return res
}

// matchOverride returns the override price for a resource. An override whose
// Target equals the full address takes precedence over one matching the type.
func matchOverride(address, typ string, overrides []settings.CostOverride) (float64, bool) {
	var typeMatch *settings.CostOverride
	for i := range overrides {
		o := &overrides[i]
		if o.Target == address {
			return o.Monthly, true
		}
		if typeMatch == nil && o.Target == typ {
			typeMatch = o
		}
	}
	if typeMatch != nil {
		return typeMatch.Monthly, true
	}
	return 0, false
}

// typeFromAddress extracts the resource type from a full address, stripping any
// leading module.<name>. segments and a data. prefix, e.g.
// "module.vpc.aws_subnet.a[0]" -> "aws_subnet".
func typeFromAddress(addr string) string {
	parts := strings.Split(addr, ".")
	for len(parts) >= 2 && parts[0] == "module" {
		parts = parts[2:]
	}
	if len(parts) > 0 && parts[0] == "data" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
