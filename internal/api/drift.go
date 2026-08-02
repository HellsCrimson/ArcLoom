package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// driftStatus is the outcome of the latest scheduled drift check for a project.
// It lives in memory only: after a restart the badge is empty until the next
// check repopulates it.
//
// "Drifted" means a plan would act: the counts are the plan's resource_changes
// (configuration vs reality), NOT every attribute that moved in the recorded
// state. Comparing against state instead flags values a provider recomputes on
// every read (updated_at, status, hashes), which never converges and never needs
// an apply.
type driftStatus struct {
	CheckedAt time.Time `json:"checkedAt"`
	Drifted   bool      `json:"drifted"`
	Add       int       `json:"add"`
	Change    int       `json:"change"`
	Destroy   int       `json:"destroy"`
	Replace   int       `json:"replace"`
	// Addresses names the drifted resources (capped at driftAddressCap) so a bare
	// count like "75 to change" can be inspected without opening the run record.
	Addresses []string `json:"addresses,omitempty"`
	// OutOfBand is how many of those resources the refresh also found changed
	// outside ArcLoom (they appear in the plan's resource_drift). The rest are
	// configuration edits that have not been applied yet.
	OutOfBand int    `json:"outOfBand,omitempty"`
	Error     string `json:"error,omitempty"`
}

// driftAddressCap bounds how many addresses a status carries: this lives in
// memory for every project and is sent with the project list.
const driftAddressCap = 25

// driftAddresses lists the resources a plan would act on, capped.
func driftAddresses(res runner.PlanResult) []string {
	out := make([]string, 0, min(len(res.Changes), driftAddressCap))
	for _, c := range res.Changes {
		if len(out) == driftAddressCap {
			break
		}
		out = append(out, c.Address)
	}
	return out
}

// outOfBandCount is how many pending changes the refresh also saw as changed
// outside ArcLoom — i.e. somebody touched the infrastructure — as opposed to a
// configuration edit nobody has applied yet.
func outOfBandCount(res runner.PlanResult) int {
	if len(res.Drift) == 0 {
		return 0
	}
	drifted := make(map[string]bool, len(res.Drift))
	for _, d := range res.Drift {
		drifted[d.Address] = true
	}
	n := 0
	for _, c := range res.Changes {
		if drifted[c.Address] {
			n++
		}
	}
	return n
}

// driftInterval clamps a stored interval to something sane; 0 (unset) falls
// back to hourly.
func driftInterval(cfg settings.Drift) time.Duration {
	if cfg.IntervalMinutes < driftMinIntervalMinutes {
		return time.Hour
	}
	return time.Duration(cfg.IntervalMinutes) * time.Minute
}

const driftMinIntervalMinutes = 5

// StartDriftScheduler runs the drift-check loop until ctx is cancelled: every
// minute (or immediately after a config change) it checks each project whose
// drift schedule is enabled and due. Checks run sequentially — this is a
// background convenience, not a fleet orchestrator — and each one serializes
// against user-triggered operations via the runner's per-workspace lock.
func (s *Server) StartDriftScheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.driftKick:
		}
		s.runDueDriftChecks(ctx)
	}
}

func (s *Server) runDueDriftChecks(ctx context.Context) {
	if s.settings == nil {
		return
	}
	for id, cfg := range s.settings.DriftAll() {
		if !cfg.Enabled || ctx.Err() != nil {
			continue
		}
		s.driftMu.Lock()
		last := s.driftStates[id].CheckedAt
		s.driftMu.Unlock()
		if time.Since(last) < driftInterval(cfg) {
			continue
		}
		s.runDriftCheck(ctx, id)
	}
}

// runDriftCheck runs one plan for a project and stores the outcome. A run-history entry is recorded only when the outcome changed since
// the previous check, so a steadily drifted (or steadily clean) project does
// not flood the capped history.
func (s *Server) runDriftCheck(ctx context.Context, projectID string) {
	p, err := s.store.Get(projectID)
	if err != nil {
		// Project is gone — drop the orphaned schedule.
		_ = s.settings.DeleteDrift(projectID)
		s.driftMu.Lock()
		delete(s.driftStates, projectID)
		s.driftMu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	defer s.beginEngineOp()() // a scheduled check counts as a run in flight

	started := time.Now()
	var logBuf strings.Builder
	logW := &lineWriter{emit: func(line string) {
		logBuf.WriteString(line)
		logBuf.WriteByte('\n')
	}}

	var res runner.PlanResult
	run, ok := s.runnerFor(string(p.Engine))
	if !ok {
		err = fmt.Errorf("engine %q unavailable", p.Engine)
	} else {
		var files map[string][]byte
		if files, err = hclgen.GenerateFiles(p); err == nil {
			var tfVars map[string]string
			tfVars, err = s.resolveTFVars(p, func(ref string) {
				fmt.Fprintf(logW, "warning: secret %s is not set\n", ref)
			})
			if err == nil {
				res, err = run.DriftPlan(ctx, s.runDir(p.ID, ""), files, tfVars, logW)
			}
		}
	}
	logW.flush()

	cur := driftStatus{CheckedAt: time.Now()}
	if err != nil {
		cur.Error = err.Error()
		slog.Warn("drift check failed", "project", projectID, "err", err)
	} else {
		cur.Drifted = res.HasChanges
		cur.Add, cur.Change, cur.Destroy, cur.Replace = res.Add, res.Change, res.Destroy, res.Replace
		cur.Addresses = driftAddresses(res)
		cur.OutOfBand = outOfBandCount(res)
	}

	s.driftMu.Lock()
	prev, hadPrev := s.driftStates[projectID]
	s.driftStates[projectID] = cur
	s.driftMu.Unlock()

	if !driftOutcomeChanged(prev, hadPrev, cur) {
		return
	}
	// News: record it and tell the outside world (a scheduled check has no user
	// watching a stream).
	s.notifyDrift(p, cur)
	rec := &runs.Run{
		ID: uuid.NewString(), ProjectID: projectID, Op: "drift",
		StartedAt: started, FinishedAt: time.Now(), Log: logBuf.String(),
	}
	if err != nil {
		rec.Status = "error"
		rec.Error = err.Error()
	} else {
		rec.Status = "success"
		rec.Plan = &res
	}
	s.recordRun(rec)
}

// driftOutcomeChanged decides whether a check result is news worth a run-history
// entry. The first check after a start is recorded only when it found something
// (drift or an error); after that any change in the drifted flag, the change
// counts, or the error is recorded.
func driftOutcomeChanged(prev driftStatus, hadPrev bool, cur driftStatus) bool {
	if !hadPrev {
		return cur.Drifted || cur.Error != ""
	}
	return prev.Drifted != cur.Drifted ||
		prev.Add != cur.Add || prev.Change != cur.Change ||
		prev.Destroy != cur.Destroy || prev.Replace != cur.Replace ||
		prev.Error != cur.Error || prev.OutOfBand != cur.OutOfBand ||
		strings.Join(prev.Addresses, ",") != strings.Join(cur.Addresses, ",")
}

// driftStatuses returns a snapshot of every project's latest drift status.
func (s *Server) driftStatuses() map[string]driftStatus {
	s.driftMu.Lock()
	defer s.driftMu.Unlock()
	out := make(map[string]driftStatus, len(s.driftStates))
	maps.Copy(out, s.driftStates)
	return out
}

// handleGetDrift returns a project's drift schedule and, when a check has run,
// its latest status.
func (s *Server) handleGetDrift(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var cfg settings.Drift
	if s.settings != nil {
		cfg = s.settings.Drift(id)
	}
	resp := map[string]any{"config": cfg}
	s.driftMu.Lock()
	st, ok := s.driftStates[id]
	s.driftMu.Unlock()
	if ok {
		resp["status"] = st
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSetDrift saves a project's drift schedule and nudges the scheduler so
// an enable takes effect immediately instead of at the next minute tick.
func (s *Server) handleSetDrift(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if s.settings == nil {
		writeErr(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	var req settings.Drift
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.IntervalMinutes == 0 {
		req.IntervalMinutes = 60
	}
	if req.IntervalMinutes < driftMinIntervalMinutes {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("interval must be at least %d minutes", driftMinIntervalMinutes))
		return
	}
	if err := s.settings.SetDrift(id, req); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Enabled {
		select {
		case s.driftKick <- struct{}{}:
		default:
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": req})
}
