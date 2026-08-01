package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/HellsCrimson/ArcLoom/internal/approvals"
	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// planFileName mirrors the runner's saved-plan filename. The workspace copy is
// transient — the next operation's sync deletes it — so it is read only in the
// window right after a plan: to stash it for approval (stashPlan) and to hand the
// policy gate the exact artifact it is about to apply.
const planFileName = "tfplan"

// runDir is the persistent CLI workspace for a project + environment. It lives
// inside the project folder (so a project backup includes its state) and is
// excluded from Git via the generated .gitignore. The empty (default)
// environment uses run/ for backward compatibility; a named environment gets its
// own run/envs/<name>/ so state never crosses environments. The env name is
// validated (validID) before it reaches this path.
func (s *Server) runDir(id, env string) string {
	base := filepath.Join(s.projectDir(id), "run")
	if env == "" {
		return base
	}
	return filepath.Join(base, "envs", env)
}

// handlePlan, handleApply and handleDestroy generate HCL for a project and
// stream init + CLI output over Server-Sent Events. Events: "log" (each output
// line), "result" (final result, plan only), "error" (failure), "done"
// (stream end). Plan is a GET (consumed via EventSource); apply and destroy
// mutate real infrastructure and are POSTs (consumed via fetch streaming).
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request)    { s.streamOp(w, r, "plan") }
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request)   { s.streamOp(w, r, "apply") }
func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) { s.streamOp(w, r, "destroy") }

// handleValidate streams a config validation (sync + init + validate). Like
// plan it is a read-only GET SSE consumed via EventSource.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) { s.streamOp(w, r, "validate") }

// handleLint streams a tflint run over the generated HCL. Read-only GET SSE like
// validate; not license-gated (tflint is a community feature).
func (s *Server) handleLint(w http.ResponseWriter, r *http.Request) { s.streamOp(w, r, "lint") }

// State surgery ops stream like plan/apply (import runs init and talks to the
// provider). Parameters arrive as query values; they are validated here so a
// bad request fails with a 400 before the SSE stream starts.
func (s *Server) handleStateImport(w http.ResponseWriter, r *http.Request) {
	if !validAddress(r.URL.Query().Get("address")) || strings.TrimSpace(r.URL.Query().Get("id")) == "" {
		writeErr(w, http.StatusBadRequest, "address and id are required")
		return
	}
	s.streamOp(w, r, "import")
}

func (s *Server) handleStateRm(w http.ResponseWriter, r *http.Request) {
	if !validAddress(r.URL.Query().Get("address")) {
		writeErr(w, http.StatusBadRequest, "address is required")
		return
	}
	s.streamOp(w, r, "state-rm")
}

// Taint / untaint mark (or clear) an instance as damaged. Untaint is the one with
// no alternative: an instance a failed create-time provisioner left tainted is
// replaced on the next apply unless the mark is cleared.
func (s *Server) handleStateTaint(w http.ResponseWriter, r *http.Request) {
	if !validAddress(r.URL.Query().Get("address")) {
		writeErr(w, http.StatusBadRequest, "address is required")
		return
	}
	s.streamOp(w, r, "taint")
}

func (s *Server) handleStateUntaint(w http.ResponseWriter, r *http.Request) {
	if !validAddress(r.URL.Query().Get("address")) {
		writeErr(w, http.StatusBadRequest, "address is required")
		return
	}
	s.streamOp(w, r, "untaint")
}

// handleStateForceUnlock releases a stuck state lock. A run the engine never
// finished (killed process, crashed container, lost connection mid-apply) leaves
// the lock held by nobody, and every later operation fails to acquire it — with
// a remote backend there is no file to delete, so this is the only remedy. The
// lock ID comes from the engine's refusal ("Lock Info: ID: …"), or from the
// state-lock endpoint when the local backend made it visible.
func (s *Server) handleStateForceUnlock(w http.ResponseWriter, r *http.Request) {
	if !validLockID(r.URL.Query().Get("lockId")) {
		writeErr(w, http.StatusBadRequest, "lockId is required")
		return
	}
	s.streamOp(w, r, "force-unlock")
}

// validLockID sanity-checks a lock ID before it becomes a CLI argument. Most
// backends use a UUID, but the format is backend-defined, so only the properties
// that make it an argument at all are enforced.
func validLockID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && len(id) <= 200 && !strings.ContainsAny(id, " \t\n")
}

// handleAcceptDrift persists the refreshed state (apply -refresh-only) so the
// state catches up to reality. It changes no infrastructure, and it is the only
// way to clear drift a refresh-only plan reports — that plan never writes back
// what it refreshed, so provider-side differences (e.g. a state adopted from
// another install, or a schema upgrade) would be reported on every check.
func (s *Server) handleAcceptDrift(w http.ResponseWriter, r *http.Request) {
	s.streamOp(w, r, "accept-drift")
}

// handleInitBackend applies a backend change to the workspace:
// ?migrate=true copies the existing state into the new backend, otherwise the
// workspace is reconfigured and starts from whatever the new backend holds.
func (s *Server) handleInitBackend(w http.ResponseWriter, r *http.Request) {
	s.streamOp(w, r, "init-backend")
}

func (s *Server) handleStateMv(w http.ResponseWriter, r *http.Request) {
	if !validAddress(r.URL.Query().Get("from")) || !validAddress(r.URL.Query().Get("to")) {
		writeErr(w, http.StatusBadRequest, "from and to are required")
		return
	}
	s.streamOp(w, r, "state-mv")
}

// validAddress is a light sanity check on a state address ("type.name",
// "module.m.type.name[0]", 'x.y["key"]'); the CLI does the real validation.
func validAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	return addr != "" && !strings.ContainsAny(addr, " \t\n")
}

func (s *Server) streamOp(w http.ResponseWriter, r *http.Request, op string) {
	p, err := s.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	run, ok := s.runnerFor(string(p.Engine))
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}
	// Resolve the target environment before the stream starts so a bad ?env=
	// fails with a 400 rather than mid-stream. Empty = the default workspace.
	envName, env, err := s.resolveEnv(p.ID, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Approval gate: a gated apply must run a plan a user with the approve
	// permission signed off on. Resolve it before streaming so "needs approval"
	// is a clean 409, not a mid-stream error, and no run record is written.
	var approved *approvals.Approval
	if op == "apply" && s.approvals != nil && s.settings.ApprovalRequired(p.ID) {
		a, ok := s.approvals.Latest(p.ID, envName)
		if !ok {
			writeErr(w, http.StatusConflict, "approval required: run a plan and have it approved before applying")
			return
		}
		approved = a
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// From here on the engine is running with resolved secrets in hand; a
	// master-key rotation must wait rather than re-seal the store underneath it.
	defer s.beginEngineOp()()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sse := &sseWriter{w: w, flusher: flusher}

	files, err := hclgen.GenerateFiles(p)
	if err != nil {
		sse.event("error", map[string]string{"error": "generate: " + err.Error()})
		return
	}
	// A named environment with backend config writes its partial `-backend-config`
	// values into the workspace; the runner passes them to init. Only meaningful
	// when the project has a backend (otherwise each env just gets its own local
	// state in run/envs/<name>/).
	if p.Backend != nil {
		bc, err := s.backendConfigFor(p, env, func(ref string) {
			sse.event("log", map[string]string{"line": "warning: backend secret " + ref + " is not set (" + op + " may fail to reach the state backend)"})
		})
		if err != nil {
			sse.event("error", map[string]string{"error": err.Error()})
			return
		}
		if bc != nil {
			files[".arcloom.tfbackend"] = bc
		}
	}
	if envName != "" {
		sse.event("log", map[string]string{"line": "environment: " + envName})
	}

	// Resolve secrets referenced by the project into TF_VAR_ injections. Values
	// are held in memory only and never written to disk or HCL.
	tfVars, err := s.resolveTFVars(p, func(ref string) {
		sse.event("log", map[string]string{"line": "warning: secret " + ref + " is not set (" + op + " may prompt or fail)"})
	})
	if err != nil {
		sse.event("error", map[string]string{"error": err.Error()})
		return
	}
	// Overlay per-env variable value overrides (TF_VAR_<name>). Secrets win on a
	// name collision (a secret-backed variable keeps its resolved value).
	for k, v := range env.Vars {
		if _, taken := tfVars[k]; !taken {
			tfVars[k] = v
		}
	}

	// Stream CLI output line-by-line as "log" events, and mirror it into a
	// buffer for the run record. The child process is killed via r.Context()
	// cancellation when the client disconnects.
	var logBuf strings.Builder
	logW := &lineWriter{emit: func(line string) {
		logBuf.WriteString(line)
		logBuf.WriteByte('\n')
		sse.event("log", map[string]string{"line": line})
	}}

	rec := &runs.Run{ID: uuid.NewString(), ProjectID: p.ID, Op: op, Env: envName, StartedAt: time.Now()}
	if u, ok := userFromContext(r); ok {
		rec.User = u.Username
	}

	dir := s.runDir(p.ID, envName)
	switch op {
	case "plan":
		var res runner.PlanResult
		res, err = run.Plan(r.Context(), dir, files, tfVars, logW)
		logW.flush()
		if err == nil {
			// A new plan supersedes any pending approval for this environment, so a
			// gated apply can never run a stale approved plan — and it becomes the
			// plan an Approve will snapshot, kept outside the workspace so the next
			// operation's sync cannot wipe it before the user clicks.
			if s.approvals != nil {
				_ = s.approvals.InvalidateEnv(p.ID, envName)
				s.stashPlan(dir, p, rec, &res)
			}
			// Best-effort cost annotation (enterprise + enabled). Runs while the
			// saved plan file still exists so Infracost can read it.
			s.attachCost(r.Context(), run, dir, p, &res, logW)
			// Policy annotation (advisory here — the gate bites on apply). Also
			// needs the saved plan file, so it runs in the same window.
			s.attachPolicy(r.Context(), run, dir, p, &res, logW)
			logW.flush()
			rec.Plan = &res
			sse.event("result", res)
		}
	case "apply":
		// planBytes is the artifact this apply will run verbatim: the approved
		// plan when the approval gate is on, or (when policies enforce) a plan
		// made here so the policy judges exactly what gets applied. Nil = plain
		// apply, which re-plans internally as before.
		var planBytes []byte
		if approved != nil {
			planBytes, err = s.approvals.Plan(p.ID, approved.ID)
		}
		if err == nil && s.policyEnforced(p.ID) {
			if planBytes == nil {
				sse.event("log", map[string]string{"line": "policy gate: planning before apply"})
				if _, err = run.Plan(r.Context(), dir, files, tfVars, logW); err == nil {
					planBytes, err = os.ReadFile(filepath.Join(dir, planFileName))
				}
				logW.flush()
			}
			if err == nil {
				var pres *runner.PolicyResult
				pres, err = s.policyGate(r.Context(), run, dir, p, planBytes, logW)
				if pres != nil {
					rec.Policy = pres
				}
			}
		}
		if err == nil {
			if planBytes != nil {
				if approved != nil {
					sse.event("log", map[string]string{"line": "applying plan approved by " + approved.ApprovedBy})
				}
				err = run.ApplyPlan(r.Context(), dir, files, tfVars, planBytes, logW)
			} else {
				err = run.Apply(r.Context(), dir, files, tfVars, logW)
			}
		}
		logW.flush()
		if err == nil {
			if approved != nil {
				_ = s.approvals.Consume(p.ID, approved.ID) // single-use
			}
			rec.Outputs = s.readOutputs(r, run, dir)
			sse.event("result", map[string]any{"outputs": rec.Outputs})
		}
	case "destroy":
		err = run.Destroy(r.Context(), dir, files, tfVars, logW)
		logW.flush()
	case "accept-drift":
		err = run.AcceptDrift(r.Context(), dir, files, tfVars, logW)
		logW.flush()
		if err == nil {
			// The stored drift status described the state we just refreshed away,
			// so drop it: the badge should clear until the next scheduled check.
			s.driftMu.Lock()
			delete(s.driftStates, p.ID)
			s.driftMu.Unlock()
		}
	case "import":
		q := r.URL.Query()
		err = run.Import(r.Context(), dir, files, tfVars, strings.TrimSpace(q.Get("address")), strings.TrimSpace(q.Get("id")), logW)
		logW.flush()
	case "state-rm":
		err = run.StateRm(r.Context(), dir, files, tfVars, strings.TrimSpace(r.URL.Query().Get("address")), logW)
		logW.flush()
	case "taint":
		err = run.Taint(r.Context(), dir, files, tfVars, strings.TrimSpace(r.URL.Query().Get("address")), logW)
		logW.flush()
	case "untaint":
		err = run.Untaint(r.Context(), dir, files, tfVars, strings.TrimSpace(r.URL.Query().Get("address")), logW)
		logW.flush()
	case "state-mv":
		q := r.URL.Query()
		err = run.StateMv(r.Context(), dir, files, tfVars, strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to")), logW)
		logW.flush()
	case "force-unlock":
		err = run.ForceUnlock(r.Context(), dir, files, strings.TrimSpace(r.URL.Query().Get("lockId")), logW)
		logW.flush()
	case "init-backend":
		err = run.InitBackend(r.Context(), dir, files, r.URL.Query().Get("migrate") == "true", logW)
		logW.flush()
	case "validate":
		var res *tfjson.ValidateOutput
		res, err = run.Validate(r.Context(), dir, files, tfVars, logW)
		logW.flush()
		if err == nil && res != nil {
			// Diagnostics go through logW so they land in both the stream and
			// the run record's log.
			for _, d := range res.Diagnostics {
				loc := ""
				if d.Range != nil {
					loc = fmt.Sprintf(" (%s:%d)", d.Range.Filename, d.Range.Start.Line)
				}
				fmt.Fprintf(logW, "%s: %s%s\n", d.Severity, d.Summary, loc)
				if d.Detail != "" {
					fmt.Fprintf(logW, "  %s\n", d.Detail)
				}
			}
			logW.flush()
			sse.event("result", map[string]any{
				"valid":        res.Valid,
				"errorCount":   res.ErrorCount,
				"warningCount": res.WarningCount,
			})
		}
	case "lint":
		var res runner.LintResult
		res, err = run.Lint(r.Context(), files, logW)
		logW.flush()
		if err == nil {
			if !res.Available {
				fmt.Fprintln(logW, "tflint is not installed on the server (PATH); no lint issues reported")
			}
			// Issues go through logW so they land in both the stream and the run
			// record's log (mirrors validate's diagnostics printing).
			for _, iss := range res.Issues {
				loc := ""
				if iss.Filename != "" {
					loc = fmt.Sprintf(" (%s:%d)", iss.Filename, iss.Line)
				}
				fmt.Fprintf(logW, "%s: %s [%s]%s\n", iss.Severity, iss.Message, iss.Rule, loc)
			}
			logW.flush()
			rec.Lint = &res
			sse.event("result", map[string]any{
				"available":    res.Available,
				"errorCount":   res.ErrorCount,
				"warningCount": res.WarningCount,
				"lint":         res,
			})
		}
	}

	rec.FinishedAt = time.Now()
	rec.Log = logBuf.String()
	if err != nil {
		rec.Status = "error"
		rec.Error = err.Error()
	} else {
		rec.Status = "success"
	}
	s.recordRun(rec)
	// Tell the outside world about an apply/destroy outcome (best-effort, in the
	// background) — the run history alone is only visible in-app.
	s.notifyRun(p, rec)

	if err != nil {
		sse.event("error", map[string]string{"error": err.Error()})
		return
	}
	sse.event("done", map[string]bool{"ok": true})
}

// resolveTFVars resolves the secrets a project references into TF_VAR_
// injections. Missing secrets are reported through onMissing (non-fatal, the
// engine may prompt or fail); resolution errors abort.
func (s *Server) resolveTFVars(p *model.Project, onMissing func(ref string)) (map[string]string, error) {
	tfVars := map[string]string{}
	for _, ref := range hclgen.CollectSecretRefs(p) {
		val, found, err := s.secrets.Resolve(p.ID, ref)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", ref, err)
		}
		if !found {
			onMissing(ref)
			continue
		}
		tfVars[model.SecretVarName(ref)] = val
	}
	return tfVars, nil
}

// backendConfigFor renders the workspace's `.arcloom.tfbackend` body: the
// project's secret-valued backend settings resolved to plaintext, plus the
// environment's own backend config. Backend secrets take this route rather than
// TF_VAR_ because Terraform forbids variables in a backend block — see
// hclgen.writeBackendAttrs. Nil when neither supplies anything.
//
// The values land on disk in the workspace (0600, inside the gitignored run/
// dir) and, once init runs, in .terraform/terraform.tfstate — unavoidable, as
// the engine needs the credential on every state read.
func (s *Server) backendConfigFor(p *model.Project, env settings.Environment, onMissing func(ref string)) ([]byte, error) {
	if p.Backend == nil {
		return nil, nil
	}
	var resolved map[string]string
	for key, ref := range hclgen.BackendSecretRefs(p.Backend) {
		val, found, err := s.secrets.Resolve(p.ID, ref)
		if err != nil {
			return nil, fmt.Errorf("backend %s: secret %s: %w", key, ref, err)
		}
		if !found {
			onMissing(ref)
			continue
		}
		if resolved == nil {
			resolved = map[string]string{}
		}
		resolved[key] = val
	}
	return backendConfigFileContents(env, resolved), nil
}

// readOutputs captures root outputs after a successful apply. Sensitive values
// are replaced before anything is stored or sent. Failure is non-fatal: the
// apply already succeeded.
func (s *Server) readOutputs(r *http.Request, run runner.Runner, dir string) []runs.Output {
	metas, err := run.Outputs(r.Context(), dir)
	if err != nil {
		slog.Warn("read outputs after apply", "err", err)
		return nil
	}
	out := make([]runs.Output, 0, len(metas))
	for name, m := range metas {
		val := string(m.Value)
		if m.Sensitive {
			val = `"(sensitive)"`
		}
		out = append(out, runs.Output{Name: name, Value: val, Sensitive: m.Sensitive})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// recordRun appends to the run history; history is best-effort and never fails
// the operation.
func (s *Server) recordRun(rec *runs.Run) {
	if s.runs == nil {
		return
	}
	if err := s.runs.Append(rec); err != nil {
		slog.Warn("record run", "project", rec.ProjectID, "run", rec.ID, "err", err)
	}
}

// handleListRuns returns a project's run history, newest first, without logs.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeJSON(w, http.StatusOK, []*runs.Run{})
		return
	}
	list, err := s.runs.List(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []*runs.Run{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleGetRun returns one full run record including its log.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	rec, err := s.runs.Get(chi.URLParam(r, "id"), chi.URLParam(r, "rid"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// sseWriter emits Server-Sent Events with immediate flushing.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseWriter) event(name string, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data)
	s.flusher.Flush()
}

// lineWriter buffers bytes and invokes emit once per complete line, so partial
// CLI writes do not produce fragmented SSE events.
type lineWriter struct {
	buf  bytes.Buffer
	emit func(string)
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		idx := bytes.IndexByte(l.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := string(l.buf.Next(idx + 1))
		l.emit(trimNewline(line))
	}
	return len(p), nil
}

func (l *lineWriter) flush() {
	if l.buf.Len() > 0 {
		l.emit(l.buf.String())
		l.buf.Reset()
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
