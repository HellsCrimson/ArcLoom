package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// bearerRoundTripper attaches the API key to every MCP HTTP request, which is
// exactly what `claude mcp add --header "Authorization: Bearer …"` does.
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b *bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// mcpTestServer spins up a real HTTP server with the MCP endpoint mounted.
func mcpTestServer(t *testing.T) (*httptest.Server, *apikey.Memory, user.Store, *store.Memory) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "one", Engine: model.EngineTofu}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.Create(&model.Project{ID: "p2", Name: "two", Engine: model.EngineTofu}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	users := user.NewMemory()
	keys := apikey.NewMemory()
	cfg := config.Config{
		AuthEnabled: true, CookieName: "arcloom_session",
		DefaultEngine: "tofu", MCPEnabled: true,
	}
	srv := New(cfg, nil, st, nil, nil, nil, auth.New(auth.NewLocalProvider(users)),
		session.NewMemory(), users, testSettings(t), nil, nil, nil, keys, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, keys, users, st
}

// mcpConnect opens an MCP session bearing token.
func mcpConnect(t *testing.T, ts *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   ts.URL + "/api/mcp",
		HTTPClient: &http.Client{Transport: &bearerRoundTripper{token: token, base: http.DefaultTransport}},
		// The server runs stateless, where GET is 405 by design; the client must
		// not try to open a standalone SSE stream.
		DisableStandaloneSSE: true,
	}
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// toolNames lists the tools the session was offered.
func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// callTool invokes one tool and returns its text content.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

// TestMCPToolsAreScopeFiltered is the headline guarantee: an agent is never
// shown a tool its credential would be 403'd on.
func TestMCPToolsAreScopeFiltered(t *testing.T) {
	ts, keys, users, _ := mcpTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources, user.PermDeploy)

	cases := []struct {
		name       string
		scopes     []string
		wantHave   []string
		wantAbsent []string
	}{
		{
			name:       "read-only key",
			scopes:     nil,
			wantHave:   []string{"list_projects", "get_project", "get_hcl", "list_state", "get_run"},
			wantAbsent: []string{"upsert_resource", "set_config", "import_hcl", "plan", "apply", "get_resource_schema"},
		},
		{
			name:   "resources key",
			scopes: []string{user.PermResources},
			wantHave: []string{
				"list_projects", "upsert_resource", "delete_resource", "set_config",
				"set_provider", "set_file", "import_hcl", "list_resource_types", "get_resource_schema",
			},
			wantAbsent: []string{"plan", "apply", "validate", "lint"},
		},
		{
			name:       "deploy key",
			scopes:     []string{user.PermDeploy},
			wantHave:   []string{"plan", "apply", "validate", "lint", "get_operation"},
			wantAbsent: []string{"upsert_resource", "get_resource_schema"},
		},
		{
			name:       "full key",
			scopes:     []string{user.PermResources, user.PermDeploy},
			wantHave:   []string{"upsert_resource", "plan", "apply", "get_resource_schema"},
			wantAbsent: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := mkKey(t, keys, "u1", tc.scopes, nil)
			names := toolNames(t, mcpConnect(t, ts, token))
			for _, want := range tc.wantHave {
				if !slices.Contains(names, want) {
					t.Errorf("tool %q missing; got %v", want, names)
				}
			}
			for _, absent := range tc.wantAbsent {
				if slices.Contains(names, absent) {
					t.Errorf("tool %q must not be advertised; got %v", absent, names)
				}
			}
		})
	}
}

// TestMCPNoDestroyTool: destroy is deliberately not reachable from MCP, at any
// scope. If someone adds it, this fails and they have to think about it.
func TestMCPNoDestroyTool(t *testing.T) {
	ts, keys, users, _ := mcpTestServer(t)
	mkUser(t, users, "u1", true)
	token := mkKey(t, keys, "u1", []string{user.PermResources, user.PermDeploy, user.PermGit, user.PermApprove}, nil)
	for _, n := range toolNames(t, mcpConnect(t, ts, token)) {
		if strings.Contains(n, "destroy") {
			t.Errorf("destroy is exposed over MCP as %q", n)
		}
	}
}

// TestMCPToolsEnforceProjectRestriction: the tool call goes through the same
// route gate as a direct HTTP call, so a project-restricted key is refused.
func TestMCPToolsEnforceProjectRestriction(t *testing.T) {
	ts, keys, users, _ := mcpTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, []string{"p1"})
	cs := mcpConnect(t, ts, token)

	out, isErr := callTool(t, cs, "get_project", map[string]any{"projectId": "p1"})
	if isErr {
		t.Errorf("in-scope project refused: %s", out)
	}
	out, isErr = callTool(t, cs, "get_project", map[string]any{"projectId": "p2"})
	if !isErr {
		t.Errorf("out-of-scope project allowed: %s", out)
	}

	// The listing must agree with the gates.
	out, isErr = callTool(t, cs, "list_projects", nil)
	if isErr {
		t.Fatalf("list_projects: %s", out)
	}
	if strings.Contains(out, `"p2"`) {
		t.Errorf("list_projects leaked an out-of-scope project: %s", out)
	}
}

// TestMCPWriteGoesThroughHandlerValidation proves the re-entry design: the
// agent gets ArcLoom's own validation error, not a generic failure, and a
// successful write really lands in the model.
func TestMCPWriteGoesThroughHandlerValidation(t *testing.T) {
	ts, keys, users, st := mcpTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, nil)
	cs := mcpConnect(t, ts, token)

	// count and for_each are mutually exclusive — a handler-level rule the MCP
	// layer never reimplements.
	out, isErr := callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource": map[string]any{
			"type": "null_resource", "name": "bad", "count": "2", "forEach": "toset([\"a\"])",
		},
	})
	if !isErr {
		t.Errorf("mutually exclusive meta-args accepted: %s", out)
	}
	if !strings.Contains(out, "mutually exclusive") {
		t.Errorf("handler's own message lost; got %q", out)
	}

	// A valid write lands in the model.
	out, isErr = callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource":  map[string]any{"type": "null_resource", "name": "good"},
	})
	if isErr {
		t.Fatalf("valid upsert failed: %s", out)
	}
	p, err := st.Get("p1")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	found := false
	for _, res := range p.Resources {
		if res.Name == "good" && res.Type == "null_resource" {
			found = true
		}
	}
	if !found {
		t.Errorf("resource not persisted; project has %d resources", len(p.Resources))
	}
}

// TestMCPRequiresCredential: no bearer, no session.
func TestMCPRequiresCredential(t *testing.T) {
	ts, _, _, _ := mcpTestServer(t)
	req, _ := http.NewRequest("POST", ts.URL+"/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated MCP: got %d, want 401", resp.StatusCode)
	}
}

// TestMCPDisabled: the endpoint is not mounted when switched off.
func TestMCPDisabled(t *testing.T) {
	users := user.NewMemory()
	cfg := config.Config{AuthEnabled: true, CookieName: "arcloom_session", DefaultEngine: "tofu", MCPEnabled: false}
	srv := New(cfg, nil, store.NewMemory(), nil, nil, nil, auth.New(auth.NewLocalProvider(users)),
		session.NewMemory(), users, testSettings(t), nil, nil, nil, apikey.NewMemory(), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("MCP endpoint answered while disabled")
	}
}

// TestFilterSchemaFields covers the attribute filter that keeps a large
// provider schema from swamping the agent's context.
func TestFilterSchemaFields(t *testing.T) {
	doc := `{"type":"aws_instance","fields":[
	  {"name":"ami","widget":"text"},
	  {"name":"instance_type","widget":"text"},
	  {"name":"tags","widget":"map"}]}`
	out, err := filterSchemaFields(doc, "ami, tags")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fields, _ := got["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("got %d fields, want 2: %s", len(fields), out)
	}
	if got["fieldsFiltered"] != true {
		t.Error("filtering not reported")
	}
	if _, err := filterSchemaFields(`{"nope":1}`, "a"); err == nil {
		t.Error("a document with no fields array should error so the caller falls back")
	}
}

// TestFilterNames covers the type-name narrowing and its truncation signal.
func TestFilterNames(t *testing.T) {
	names := []string{"aws_instance", "aws_instances_data", "aws_s3_bucket", "aws_vpc"}
	got, trunc := filterNames(names, "instance", 10)
	if len(got) != 2 || trunc {
		t.Errorf("filter: got %v trunc=%v", got, trunc)
	}
	got, trunc = filterNames(names, "", 2)
	if len(got) != 2 || !trunc {
		t.Errorf("cap must report truncation: got %v trunc=%v", got, trunc)
	}
}

// TestParseProviderTypes locks the endpoint choice for list_resource_types.
//
// Regression: the tool originally called GET /providers/{source}/schema, which
// returns the provider's own *config* schema. That decoded cleanly into the
// type-listing struct as all-zero values, so every provider came back with no
// resource types and no error — the failure was completely silent. A document
// with no resourceTypes key must therefore be an error, not an empty answer.
func TestParseProviderTypes(t *testing.T) {
	// What POST /providers actually returns.
	listing := `{"provider":"registry.opentofu.org/hashicorp/local",
	             "resourceTypes":["local_file","local_sensitive_file"],
	             "dataSourceTypes":["local_file"]}`
	doc, err := parseProviderTypes(listing)
	if err != nil {
		t.Fatalf("valid listing rejected: %v", err)
	}
	if len(doc.ResourceTypes) != 2 || doc.Provider == "" {
		t.Errorf("listing mis-parsed: %+v", doc)
	}

	// What GET /providers/{source}/schema returns — the wrong endpoint. It is
	// valid JSON and would decode to zero values, so it must be refused.
	configSchema := `{"provider":"registry.opentofu.org/hashicorp/local",
	                  "resourceType":"hashicorp/local","version":0,"fields":[]}`
	if _, err := parseProviderTypes(configSchema); err == nil {
		t.Error("the provider config schema must not parse as a type listing")
	}

	// A provider genuinely offering nothing is still a legal answer.
	if _, err := parseProviderTypes(`{"provider":"x","resourceTypes":[]}`); err != nil {
		t.Errorf("an empty but well-formed listing was rejected: %v", err)
	}
	if _, err := parseProviderTypes(`not json`); err == nil {
		t.Error("malformed body should error")
	}
}

// TestMCPDoesNotAdvertiseListChanged: the SDK infers tools.listChanged=true the
// moment a tool is registered, but a Stateless server has no channel to deliver
// that notification (no standalone SSE stream; GET is 405). Advertising it
// invites a client to wait for a message that can never arrive.
func TestMCPDoesNotAdvertiseListChanged(t *testing.T) {
	ts, keys, users, _ := mcpTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, nil)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` +
		`"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"t","version":"1"}}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, `"tools"`) {
		t.Fatalf("initialize did not advertise tools at all: %s", got)
	}
	if strings.Contains(got, `"listChanged":true`) {
		t.Errorf("advertised a notification a stateless server cannot send: %s", got)
	}
}
