package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
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

// apiResult is the outcome of a re-entrant call.
type apiResult struct {
	Status int
	Body   string
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
