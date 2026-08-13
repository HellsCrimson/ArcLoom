package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// This file is the bridge between an MCP tool call and the existing REST
// handlers.
//
// Tool handlers do NOT touch the store directly. They re-enter the chi router
// with a synthetic request, because all of the request validation lives inside
// the handlers (handleUpsertResource alone does body decode, count/for_each
// mutual exclusion, hclgen.ValidateExpr, the provider-ref regex and block
// conversion before it reaches store.Update). Re-entering inherits every one of
// those checks, plus requireAuth, requireProjectPerm, the per-project ACL, the
// API-key scope gate and the audit middleware — none of which has to be
// reimplemented or kept in sync.
//
// The cost is one extra trip through the router per tool call, which is
// nothing next to an engine run.

// bufWriter is an http.ResponseWriter that accumulates the response in memory.
// It implements http.Flusher because the deploy routes are SSE handlers that
// flush as they stream; without it they would panic on the type assertion.
//
// Concurrency: a streaming handler writes from the op goroutine while the MCP
// tool call reads the buffer to report progress, so every access is behind mu.
type bufWriter struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
	status int
	done   bool
}

func newBufWriter() *bufWriter {
	return &bufWriter{header: http.Header{}, status: http.StatusOK}
}

func (b *bufWriter) Header() http.Header { return b.header }

func (b *bufWriter) WriteHeader(code int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = code
}

func (b *bufWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *bufWriter) Flush() {}

// snapshot returns the response so far.
func (b *bufWriter) snapshot() (int, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, b.buf.String()
}

// finish marks the response complete.
func (b *bufWriter) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.done = true
}

func (b *bufWriter) finished() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.done
}

// mcpOriginCtxKey marks a request as having been made by an MCP tool call, so
// the audit trail can tell an agent's write apart from the same user doing the
// same thing in the browser.
//
// It is deliberately a **context value set here, in-process**, not a header.
// A header (`X-ArcLoom-Via: mcp`) would be settable by any external client,
// which would let anyone write a false origin into the audit log — the one
// place that must not be forgeable. Nothing outside this package can reach this
// key, so the marker is only ever true for a request this file constructed.
type mcpOriginCtxKey struct{}

// viaMCP reports whether the request originated from an MCP tool call.
func viaMCP(r *http.Request) bool {
	v, _ := r.Context().Value(mcpOriginCtxKey{}).(bool)
	return v
}

// apiResult is the outcome of a re-entrant call.
type apiResult struct {
	Status int
	Body   string
}

// --- response shaping ---
//
// Most write handlers answer with the whole project, which is right for the SPA
// (it re-renders from that document) and wrong for a tool result: a real project
// runs to six figures of characters, so an agent that writes four locals pays
// for four copies of everything it did not touch, and each one can overflow the
// client's tool-result limit. The MCP layer therefore reduces a full-project
// response to an acknowledgement. The REST contract is untouched.

// maxToolWarnings caps the advisory list so a project with a systematic problem
// cannot turn every write into a wall of text.
const maxToolWarnings = 20

// projectCounts reduces a project document to "what is in it now" — enough for
// an agent to confirm the write landed and notice a list it did not mean to
// clear, without shipping the model back.
func projectCounts(p *model.Project) map[string]int {
	counts := map[string]int{
		"providers":   len(p.Providers),
		"resources":   len(p.Resources),
		"dataSources": len(p.DataSources),
		"variables":   len(p.Variables),
		"locals":      len(p.Locals),
		"outputs":     len(p.Outputs),
		"modules":     len(p.Modules),
		"files":       len(p.Files),
		"imports":     len(p.Imports),
		"moved":       len(p.Moved),
	}
	if len(p.LocalModules) > 0 {
		counts["localModules"] = len(p.LocalModules)
	}
	return counts
}

// modelWarnings renders the project's dangling var./local. references as advice.
//
// Advisory rather than fatal on purpose: writes have no required order, so a
// local naming a variable the next call declares is a legitimate intermediate
// state (see hclgen.UndeclaredRefs). What it prevents is the silent version —
// config that generates cleanly and fails at plan time on a variable nothing
// declares, with nothing between the write and the failure to say so.
func modelWarnings(p *model.Project) []string {
	refs := hclgen.UndeclaredRefs(p)
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if len(out) == maxToolWarnings {
			out = append(out, fmt.Sprintf("…and %d more undeclared references", len(refs)-maxToolWarnings))
			break
		}
		out = append(out, ref.String())
	}
	return out
}

// projectFromBody decodes a full-project response.
func projectFromBody(body string) (*model.Project, error) {
	var p model.Project
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// warningsFor re-reads a project to check it after a write whose own response
// is too small to check (upsert_resource answers with one resource). It goes
// back through the router, so the read is authorized like any other.
func (s *Server) warningsFor(ctx context.Context, orig *http.Request, projectID string) []string {
	res := s.callAPI(ctx, orig, "GET", "/api/projects/"+url.PathEscape(projectID), nil)
	if !res.ok() {
		return nil
	}
	p, err := projectFromBody(res.Body)
	if err != nil {
		return nil
	}
	return modelWarnings(p)
}

// ok reports whether the call succeeded.
func (r apiResult) ok() bool { return r.Status >= 200 && r.Status < 300 }

// callAPI issues a synthetic request against the router, carrying the calling
// MCP request's credential so the handlers authenticate and authorize it
// exactly as they would a direct HTTP call.
//
// path must already include the /api prefix and any query string.
func (s *Server) callAPI(ctx context.Context, orig *http.Request, method, path string, body []byte) apiResult {
	w := newBufWriter()
	req := s.syntheticRequest(ctx, orig, method, path, body)
	if req == nil {
		return apiResult{Status: http.StatusInternalServerError, Body: `{"error":"could not build request"}`}
	}
	s.router().ServeHTTP(w, req)
	status, out := w.snapshot()
	return apiResult{Status: status, Body: out}
}

// syntheticRequest builds the re-entrant request. The credential is copied
// verbatim — the Authorization header for a key, the session cookie for a
// browser — so the inner call is authenticated by the same thing that
// authenticated the outer one and can never be more privileged than it.
func (s *Server) syntheticRequest(ctx context.Context, orig *http.Request, method, path string, body []byte) *http.Request {
	u, err := url.Parse(path)
	if err != nil {
		return nil
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	// Drop any chi routing context inherited from the outer request.
	//
	// This is load-bearing, not hygiene. chi's Mux.ServeHTTP reuses an existing
	// *chi.Context when it finds one ("a routing context already exists from a
	// parent router") and does NOT reset it — so the synthetic request would be
	// routed against the /api/mcp match that is already in there and 404 on
	// every path. Storing an untyped nil makes chi's type assertion fail, which
	// sends it down the fresh-context path; cancellation still propagates,
	// which a context.Background() would have thrown away.
	ctx = context.WithValue(ctx, chi.RouteCtxKey, nil)
	// Mark the origin for the audit trail (see mcpOriginCtxKey).
	ctx = context.WithValue(ctx, mcpOriginCtxKey{}, true)

	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil
	}
	req.Host = orig.Host
	req.RemoteAddr = orig.RemoteAddr
	if v := orig.Header.Get("Authorization"); v != "" {
		req.Header.Set("Authorization", v)
	}
	if c, err := orig.Cookie(s.cfg.CookieName); err == nil {
		req.AddCookie(c)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// startAPICall runs a streaming route (plan/apply/validate/lint/state ops) in
// the background and returns the live buffer plus a cancel function.
//
// The op deliberately does NOT inherit the MCP request's context: that context
// dies the moment the tool call returns, which is immediately — the whole point
// of returning an operation id instead of blocking. It gets its own bounded
// context so a wedged engine run cannot leak a goroutine forever.
func (s *Server) startAPICall(orig *http.Request, method, path string, body []byte, timeout time.Duration) (*bufWriter, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	w := newBufWriter()
	req := s.syntheticRequest(ctx, orig, method, path, body)
	if req == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"could not build request"}`))
		w.finish()
		cancel()
		return w, func() {}
	}
	go func() {
		defer cancel()
		defer w.finish()
		s.router().ServeHTTP(w, req)
	}()
	return w, cancel
}
