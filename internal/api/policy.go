package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// policyDocNameRe constrains a Rego document's file name: it is written into a
// throwaway policy directory, so no separators, no leading dot, no traversal.
var policyDocNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// maxPolicyDocs bounds how many Rego documents a project can store (each one is
// written to disk and compiled on every check).
const maxPolicyDocs = 50

// handleGetPolicy returns a project's policy-as-code config (any authenticated
// project reader; Rego source is documentation, not a secret).
func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": s.policyConfig(id)})
}

// handleSetPolicy saves a project's policy-as-code config (resources perm).
// Not license-gated: the policy gate is community/Apache core, like the plan →
// approve → apply gate it complements.
func (s *Server) handleSetPolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if s.settings == nil {
		writeErr(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	var req settings.Policy
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	// Normalize + validate: drop empty rows, keep file names safe (they become
	// files in the policy directory) and unique.
	cleaned := make([]settings.PolicyDoc, 0, len(req.Docs))
	seen := map[string]bool{}
	for i, d := range req.Docs {
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" && strings.TrimSpace(d.Rego) == "" {
			continue
		}
		if d.Name == "" {
			d.Name = fmt.Sprintf("policy-%d.rego", i+1)
		}
		if !strings.HasSuffix(d.Name, ".rego") {
			d.Name += ".rego"
		}
		if !policyDocNameRe.MatchString(d.Name) || strings.Contains(d.Name, "..") {
			writeErr(w, http.StatusBadRequest, "invalid policy name: "+d.Name)
			return
		}
		if seen[d.Name] {
			writeErr(w, http.StatusBadRequest, "duplicate policy name: "+d.Name)
			return
		}
		if strings.TrimSpace(d.Rego) == "" {
			writeErr(w, http.StatusBadRequest, "policy "+d.Name+" is empty")
			return
		}
		seen[d.Name] = true
		cleaned = append(cleaned, d)
	}
	if len(cleaned) > maxPolicyDocs {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("at most %d policies per project", maxPolicyDocs))
		return
	}
	req.Docs = cleaned
	if err := s.settings.SetPolicy(id, req); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": s.policyConfig(id)})
}

// policyConfig returns a project's policy config with Docs never nil (so the
// JSON carries an array, not null).
func (s *Server) policyConfig(projectID string) settings.Policy {
	var cfg settings.Policy
	if s.settings != nil {
		cfg = s.settings.Policy(projectID)
	}
	if cfg.Docs == nil {
		cfg.Docs = []settings.PolicyDoc{}
	}
	return cfg
}

// policyDocs renders the stored documents as the name -> source map the runner
// writes into its throwaway policy directory.
func policyDocs(cfg settings.Policy) map[string][]byte {
	docs := make(map[string][]byte, len(cfg.Docs))
	for _, d := range cfg.Docs {
		docs[d.Name] = []byte(d.Rego)
	}
	return docs
}

// policyBlocks reports whether a check result stops an apply: any failure (a
// deny rule), or a warning when the project promotes warnings to blocking.
func policyBlocks(res *runner.PolicyResult, cfg settings.Policy) bool {
	if res == nil {
		return false
	}
	if res.FailureCount > 0 {
		return true
	}
	return cfg.FailOnWarn && res.WarningCount > 0
}

// policyEnforced reports whether an apply for this project must pass a policy
// check before it runs.
func (s *Server) policyEnforced(projectID string) bool {
	cfg := s.policyConfig(projectID)
	return cfg.Enabled && cfg.Enforce && len(cfg.Docs) > 0
}

// attachPolicy runs the project's policies against the plan just saved in dir
// and attaches the result to res.Policy (advisory: a plan is never blocked).
// Called while <dir>/tfplan still exists, like attachCost.
func (s *Server) attachPolicy(ctx context.Context, run runner.Runner, dir string, p *model.Project, res *runner.PlanResult, logW *lineWriter) {
	cfg := s.policyConfig(p.ID)
	if !cfg.Enabled || len(cfg.Docs) == 0 {
		return
	}
	pres := s.runPolicy(ctx, run, dir, cfg, nil, logW)
	res.Policy = &pres
}

// runPolicy executes the policy check and mirrors its findings into the streamed
// log (so they land in the run record too), mirroring how validate/lint print
// their diagnostics. planBytes selects the artifact: nil = the workspace's saved
// plan, non-nil = an explicit (approved) plan.
func (s *Server) runPolicy(ctx context.Context, run runner.Runner, dir string, cfg settings.Policy, planBytes []byte, logW *lineWriter) runner.PolicyResult {
	res, err := run.Policy(ctx, dir, planBytes, policyDocs(cfg), logW)
	if err != nil {
		res.Error = firstLine(err.Error())
	}
	logW.flush()
	switch {
	case !res.Available:
		fmt.Fprintln(logW, "policy: conftest is not installed on the server (PATH); no policies evaluated")
	case res.Error != "":
		fmt.Fprintln(logW, "policy: check could not run: "+res.Error)
	default:
		for _, v := range res.Violations {
			fmt.Fprintf(logW, "policy %s: %s\n", v.Severity, v.Message)
		}
		if len(res.Violations) == 0 {
			fmt.Fprintf(logW, "policy: %d rule(s) passed\n", res.Successes)
		}
	}
	logW.flush()
	return res
}

// policyGate is the enforcing check an apply must pass. It returns the result
// (nil when the gate is off) and a non-nil error when the apply must be
// blocked. It fails CLOSED: a missing conftest binary or a check that could not
// run blocks, so a broken policy setup cannot silently wave applies through.
func (s *Server) policyGate(ctx context.Context, run runner.Runner, dir string, p *model.Project, planBytes []byte, logW *lineWriter) (*runner.PolicyResult, error) {
	cfg := s.policyConfig(p.ID)
	if !cfg.Enabled || !cfg.Enforce || len(cfg.Docs) == 0 {
		return nil, nil
	}
	res := s.runPolicy(ctx, run, dir, cfg, planBytes, logW)
	switch {
	case !res.Available:
		return &res, fmt.Errorf("policy enforcement is enabled but conftest is not installed on the server")
	case res.Error != "":
		return &res, fmt.Errorf("policy check could not run: %s", res.Error)
	case policyBlocks(&res, cfg):
		return &res, fmt.Errorf("policy check failed: %d violation(s) block this apply", blockingCount(&res, cfg))
	}
	return &res, nil
}

// blockingCount is how many violations caused the block (warnings count only
// when the project promotes them).
func blockingCount(res *runner.PolicyResult, cfg settings.Policy) int {
	n := res.FailureCount
	if cfg.FailOnWarn {
		n += res.WarningCount
	}
	return n
}

// firstLine trims a multi-line error down to its first line for display.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
