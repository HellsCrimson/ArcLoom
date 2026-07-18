package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// runDir is the persistent CLI workspace for a project. It lives inside the
// project folder (so a project backup includes its state) and is excluded from
// Git via the generated .gitignore.
func (s *Server) runDir(id string) string {
	return filepath.Join(s.projectDir(id), "run")
}

// handlePlan, handleApply and handleDestroy generate HCL for a project and
// stream init + CLI output over Server-Sent Events. Events: "log" (each output
// line), "result" (final result, plan only), "error" (failure), "done"
// (stream end). Plan is a GET (consumed via EventSource); apply and destroy
// mutate real infrastructure and are POSTs (consumed via fetch streaming).
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request)    { s.streamOp(w, r, "plan") }
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request)  { s.streamOp(w, r, "apply") }
func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) { s.streamOp(w, r, "destroy") }

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
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

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

	// Resolve secrets referenced by the project into TF_VAR_ injections. Values
	// are held in memory only and never written to disk or HCL.
	tfVars := map[string]string{}
	for _, ref := range hclgen.CollectSecretRefs(p) {
		val, found, serr := s.secrets.Resolve(p.ID, ref)
		if serr != nil {
			sse.event("error", map[string]string{"error": "secret " + ref + ": " + serr.Error()})
			return
		}
		if !found {
			sse.event("log", map[string]string{"line": "warning: secret " + ref + " is not set (" + op + " may prompt or fail)"})
			continue
		}
		tfVars[model.SecretVarName(ref)] = val
	}

	// Stream CLI output line-by-line as "log" events. The child process is
	// killed via r.Context() cancellation when the client disconnects.
	logW := &lineWriter{emit: func(line string) { sse.event("log", map[string]string{"line": line}) }}

	dir := s.runDir(p.ID)
	switch op {
	case "plan":
		var res any
		res, err = run.Plan(r.Context(), dir, files, tfVars, logW)
		logW.flush()
		if err == nil {
			sse.event("result", res)
		}
	case "apply":
		err = run.Apply(r.Context(), dir, files, tfVars, logW)
		logW.flush()
	case "destroy":
		err = run.Destroy(r.Context(), dir, files, tfVars, logW)
		logW.flush()
	}
	if err != nil {
		sse.event("error", map[string]string{"error": err.Error()})
		return
	}
	sse.event("done", map[string]bool{"ok": true})
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
