package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/audit"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// auditAttrServer builds a server with a real audit store so entries can be
// read back.
func auditAttrServer(t *testing.T) (*httptest.Server, *apikey.Memory, user.Store, audit.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "one", Engine: model.EngineTofu}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	users := user.NewMemory()
	keys := apikey.NewMemory()
	auditLog, err := audit.NewDisk(t.TempDir())
	if err != nil {
		t.Fatalf("audit store: %v", err)
	}
	cfg := config.Config{
		AuthEnabled: true, CookieName: "arcloom_session",
		DefaultEngine: "tofu", MCPEnabled: true,
	}
	srv := New(cfg, nil, st, nil, nil, nil, auth.New(auth.NewLocalProvider(users)),
		session.NewMemory(), users, testSettings(t), nil, auditLog, nil, keys, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, keys, users, auditLog
}

// lastUpsert returns the most recent resource.upsert entry.
func lastUpsert(t *testing.T, log audit.Store) *audit.Entry {
	t.Helper()
	entries, err := log.List(audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	for _, e := range entries {
		if e.Action == "resource.upsert" {
			return e
		}
	}
	t.Fatal("no resource.upsert entry recorded")
	return nil
}

// TestAuditRecordsOriginAndKey: the same account acting from three surfaces must
// be distinguishable in the trail.
func TestAuditRecordsOriginAndKey(t *testing.T) {
	ts, keys, users, log := auditAttrServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, nil)

	// 1. A direct API call with the key → origin "api", key named.
	req, _ := http.NewRequest("POST", ts.URL+"/api/projects/p1/resources",
		strings.NewReader(`{"type":"null_resource","name":"direct"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("direct write: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct write: got %d", resp.StatusCode)
	}
	e := lastUpsert(t, log)
	if e.Via != audit.OriginAPI {
		t.Errorf("direct API call recorded Via=%q, want %q", e.Via, audit.OriginAPI)
	}
	if e.KeyLabel != "t" || e.KeyID == "" {
		t.Errorf("key not attributed: label=%q id=%q", e.KeyLabel, e.KeyID)
	}
	if e.User != "u1" {
		t.Errorf("User=%q, want u1", e.User)
	}

	// 2. The same key through an MCP tool call → origin "mcp".
	cs := mcpConnect(t, ts, token)
	out, isErr := callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource":  map[string]any{"type": "null_resource", "name": "viamcp"},
	})
	if isErr {
		t.Fatalf("mcp write failed: %s", out)
	}
	e = lastUpsert(t, log)
	if e.Via != audit.OriginMCP {
		t.Errorf("MCP tool call recorded Via=%q, want %q", e.Via, audit.OriginMCP)
	}
	if e.KeyLabel != "t" {
		t.Errorf("MCP call lost the key label: %q", e.KeyLabel)
	}
}

// TestAuditOriginCannotBeForged is why the MCP marker is a context value rather
// than a header: the audit trail is the one place a client must not be able to
// write a lie about itself.
func TestAuditOriginCannotBeForged(t *testing.T) {
	ts, keys, users, log := auditAttrServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, nil)

	req, _ := http.NewRequest("POST", ts.URL+"/api/projects/p1/resources",
		strings.NewReader(`{"type":"null_resource","name":"liar"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	// Every header shape someone might try to spoof the origin with.
	for _, h := range []string{"X-ArcLoom-Via", "X-Arcloom-Origin", "Via"} {
		req.Header.Set(h, "mcp")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	resp.Body.Close()

	if e := lastUpsert(t, log); e.Via != audit.OriginAPI {
		t.Errorf("a forged header changed the recorded origin to %q", e.Via)
	}
}

// TestViaMCPDefaultsFalse: an ordinary request carries no marker, so the helper
// cannot accidentally report MCP for browser traffic.
func TestViaMCPDefaultsFalse(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/projects", nil)
	if viaMCP(r) {
		t.Error("a plain request must not read as MCP")
	}
	r = r.WithContext(context.WithValue(r.Context(), mcpOriginCtxKey{}, true))
	if !viaMCP(r) {
		t.Error("an in-process marked request must read as MCP")
	}
}
