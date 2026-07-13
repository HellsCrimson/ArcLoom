package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/matthiasrauline/arcloom/internal/hclgen"
)

// handlePlan generates HCL for a project and streams init + plan output over
// Server-Sent Events. Events: "log" (each output line), "result" (final
// PlanResult), "error" (failure), "done" (stream end).
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
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

	hcl, err := hclgen.Generate(p)
	if err != nil {
		sse.event("error", map[string]string{"error": "generate: " + err.Error()})
		return
	}

	// Stream CLI output line-by-line as "log" events. The child process is
	// killed via r.Context() cancellation when the client disconnects.
	logW := &lineWriter{emit: func(line string) { sse.event("log", map[string]string{"line": line}) }}

	res, err := run.Plan(r.Context(), hcl, p.Providers, logW)
	logW.flush()
	if err != nil {
		sse.event("error", map[string]string{"error": err.Error()})
		return
	}
	sse.event("result", res)
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
