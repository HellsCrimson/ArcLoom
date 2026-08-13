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
			wantAbsent: []string{"upsert_resource", "set_config", "import_hcl", "plan", "apply", "get_resource_schema", "set_moved", "move_state", "create_local_module"},
		},
		{
			name:   "resources key",
			scopes: []string{user.PermResources},
			wantHave: []string{
				"list_projects", "upsert_resource", "delete_resource", "set_config",
				"set_provider", "set_file", "delete_file", "import_hcl", "list_resource_types", "get_resource_schema",
				"set_moved", "set_imports",
				"create_local_module", "set_module_call", "delete_module_call", "delete_local_module",
			},
			// move_state runs the engine, so it is a deploy tool even though the
			// refactor it belongs to is a resources one.
			wantAbsent: []string{"plan", "apply", "validate", "lint", "move_state"},
		},
		{
			name:       "deploy key",
			scopes:     []string{user.PermDeploy},
			wantHave:   []string{"plan", "apply", "validate", "lint", "get_operation", "move_state"},
			wantAbsent: []string{"upsert_resource", "get_resource_schema", "set_moved"},
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

// resourcesSession is the common fixture for the write tools: a key that may
// edit p1 and nothing else to set up.
func resourcesSession(t *testing.T) (*mcp.ClientSession, *store.Memory) {
	t.Helper()
	ts, keys, users, st := mcpTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, nil)
	return mcpConnect(t, ts, token), st
}

// decode parses a tool result, failing the test on anything that is not JSON.
func decode(t *testing.T, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("tool result is not JSON (%v): %s", err, out)
	}
	return doc
}

// TestMCPMovedBlocksRoundTrip: a refactor that renames addresses is only
// non-destructive if the rename is expressible, which is what set_moved is for.
func TestMCPMovedBlocksRoundTrip(t *testing.T) {
	cs, st := resourcesSession(t)

	out, isErr := callTool(t, cs, "set_moved", map[string]any{
		"projectId": "p1",
		"moved": []map[string]any{
			{"from": "null_resource.affine", "to": `null_resource.main["affine"]`},
			{"from": "null_resource.qbit", "to": `null_resource.main["qbit"]`},
		},
	})
	if isErr {
		t.Fatalf("set_moved: %s", out)
	}
	if counts := decode(t, out)["counts"].(map[string]any); counts["moved"].(float64) != 2 {
		t.Errorf("expected 2 moved blocks in the ack, got %v", counts["moved"])
	}
	p, _ := st.Get("p1")
	if len(p.Moved) != 2 || p.Moved[0].From != "null_resource.affine" {
		t.Fatalf("model did not record the blocks: %+v", p.Moved)
	}
	// Every block carries an id, so a later call can keep the ones it means to.
	if p.Moved[0].ID == "" {
		t.Error("moved block has no id")
	}

	// The list is replaced, which is how blocks are removed once applied.
	if out, isErr = callTool(t, cs, "set_moved", map[string]any{"projectId": "p1", "moved": []map[string]any{}}); isErr {
		t.Fatalf("clear: %s", out)
	}
	if p, _ = st.Get("p1"); len(p.Moved) != 0 {
		t.Errorf("blocks survived the clear: %+v", p.Moved)
	}
}

// A moved block takes a STATIC address; the engine resolves it before
// evaluating anything, so an each.key index can never work. Re-entering the
// REST handler is what gets that refusal for free.
func TestMCPMovedRejectsComputedAddress(t *testing.T) {
	cs, _ := resourcesSession(t)
	out, isErr := callTool(t, cs, "set_moved", map[string]any{
		"projectId": "p1",
		"moved":     []map[string]any{{"from": "null_resource.a", "to": `null_resource.main[each.key]`}},
	})
	if !isErr {
		t.Fatalf("computed index accepted: %s", out)
	}
	if !strings.Contains(out, "address") {
		t.Errorf("unhelpful refusal: %s", out)
	}
}

// TestMCPImportBlocksRoundTrip: the bulk adoption path, whose id may be an
// expression (which a CLI import can never take).
func TestMCPImportBlocksRoundTrip(t *testing.T) {
	cs, st := resourcesSession(t)
	out, isErr := callTool(t, cs, "set_imports", map[string]any{
		"projectId": "p1",
		"imports": []map[string]any{{
			"to": "null_resource.main[each.key]", "id": "each.value", "forEach": `toset(["a","b"])`,
		}},
	})
	if isErr {
		t.Fatalf("set_imports: %s", out)
	}
	p, _ := st.Get("p1")
	if len(p.Imports) != 1 || p.Imports[0].ImportID != "each.value" || p.Imports[0].ForEach == "" {
		t.Fatalf("import block not stored as sent: %+v", p.Imports)
	}
}

// TestMCPModuleTools: a definition and a call are different things, and a
// refactor into a module needs both.
func TestMCPModuleTools(t *testing.T) {
	cs, st := resourcesSession(t)

	if out, isErr := callTool(t, cs, "create_local_module", map[string]any{
		"projectId": "p1", "name": "team", "call": false,
	}); isErr {
		t.Fatalf("create_local_module: %s", out)
	}
	p, _ := st.Get("p1")
	if len(p.LocalModules) != 1 || p.LocalModules[0].Name != "team" {
		t.Fatalf("definition not created: %+v", p.LocalModules)
	}
	if len(p.Modules) != 0 {
		t.Fatalf("call:false still created a call: %+v", p.Modules)
	}

	if out, isErr := callTool(t, cs, "set_module_call", map[string]any{
		"projectId": "p1",
		"call":      map[string]any{"name": "eu", "source": "./modules/team", "forEach": `toset(["a"])`},
	}); isErr {
		t.Fatalf("set_module_call: %s", out)
	}
	p, _ = st.Get("p1")
	if len(p.Modules) != 1 || p.Modules[0].Name != "eu" || p.Modules[0].ForEach == "" {
		t.Fatalf("call not created with its meta-arg: %+v", p.Modules)
	}

	// The definition is in use, so deleting it is refused rather than silently
	// leaving a call pointing at a folder that no longer exists.
	out, isErr := callTool(t, cs, "delete_local_module", map[string]any{"projectId": "p1", "name": "team"})
	if !isErr {
		t.Fatalf("deleted a module still in use: %s", out)
	}
	if out, isErr = callTool(t, cs, "delete_local_module", map[string]any{
		"projectId": "p1", "name": "team", "force": true,
	}); isErr {
		t.Fatalf("forced delete: %s", out)
	}
	if p, _ = st.Get("p1"); len(p.LocalModules) != 0 || len(p.Modules) != 0 {
		t.Fatalf("force did not take the calls along: %+v / %+v", p.LocalModules, p.Modules)
	}
}

// TestMCPWritesDoNotReturnTheProject: the REST handlers answer with the whole
// model, which is right for the SPA and ruinous as a tool result — a real
// project runs past the client's per-result limit on every write.
func TestMCPWritesDoNotReturnTheProject(t *testing.T) {
	cs, st := resourcesSession(t)
	// Give the project enough content that passing it through would show.
	if err := st.Update("p1", func(p *model.Project) error {
		for i := range 40 {
			p.Resources = append(p.Resources, model.Resource{
				ID: "r" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Type: "null_resource",
				Name:  "n" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
				Attrs: map[string]model.AttrValue{"triggers": {Mode: model.ModeExpression, Expr: `timestamp()`}},
			})
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, isErr := callTool(t, cs, "set_config", map[string]any{
		"projectId": "p1",
		"config":    map[string]any{"locals": []map[string]any{{"name": "a", "value": map[string]any{"mode": "literal", "literal": "x"}}}},
	})
	if isErr {
		t.Fatalf("set_config: %s", out)
	}
	if strings.Contains(out, "null_resource") {
		t.Errorf("the whole project came back in the tool result: %s", out)
	}
	doc := decode(t, out)
	if doc["ok"] != true || doc["projectId"] != "p1" {
		t.Errorf("ack does not confirm the write: %s", out)
	}
	counts, ok := doc["counts"].(map[string]any)
	if !ok || counts["resources"].(float64) != 40 || counts["locals"].(float64) != 1 {
		t.Errorf("ack does not report what the project now holds: %s", out)
	}
}

// TestMCPWarnsOnUndeclaredVariable: nothing derives a `variable` block from an
// expression, so `var.x` typed into one generates config that fails at plan
// time on a variable no file declares. The write still lands — declaration
// order is the caller's business — but it does not land silently.
func TestMCPWarnsOnUndeclaredVariable(t *testing.T) {
	cs, _ := resourcesSession(t)
	out, isErr := callTool(t, cs, "set_config", map[string]any{
		"projectId": "p1",
		"config": map[string]any{"locals": []map[string]any{
			{"name": "probe", "value": map[string]any{"mode": "expression", "expr": "var.zzz_probe_only"}},
		}},
	})
	if isErr {
		t.Fatalf("set_config: %s", out)
	}
	warnings, _ := decode(t, out)["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "var.zzz_probe_only") {
		t.Fatalf("no warning about the undeclared variable: %s", out)
	}

	// Declaring it clears the warning — the check is about the model, not about
	// which call wrote what.
	out, isErr = callTool(t, cs, "set_config", map[string]any{
		"projectId": "p1",
		"config": map[string]any{
			"variables": []map[string]any{{"name": "zzz_probe_only", "type": "string"}},
			"locals": []map[string]any{
				{"name": "probe", "value": map[string]any{"mode": "expression", "expr": "var.zzz_probe_only"}},
			},
		},
	})
	if isErr {
		t.Fatalf("set_config: %s", out)
	}
	if _, ok := decode(t, out)["warnings"]; ok {
		t.Errorf("warned about a declared variable: %s", out)
	}
}

// upsert_resource keeps its small response, and carries the same warning.
func TestMCPUpsertResourceWarnsAndStaysSmall(t *testing.T) {
	cs, _ := resourcesSession(t)
	out, isErr := callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource":  map[string]any{"type": "null_resource", "name": "web", "forEach": "local.absent"},
	})
	if isErr {
		t.Fatalf("upsert: %s", out)
	}
	doc := decode(t, out)
	res, ok := doc["resource"].(map[string]any)
	if !ok || res["name"] != "web" || res["id"] == "" {
		t.Fatalf("the saved resource (with its id) must come back: %s", out)
	}
	warnings, _ := doc["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "local.absent") {
		t.Errorf("no warning about the undeclared local: %s", out)
	}
}

// TestMCPUpsertRefusesDuplicateAddress: type+name IS the HCL address, so a
// second holder of one generates two identical blocks — invalid config that
// nothing notices until a plan, several writes later.
func TestMCPUpsertRefusesDuplicateAddress(t *testing.T) {
	cs, st := resourcesSession(t)
	out, isErr := callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource":  map[string]any{"type": "null_resource", "name": "qbittorrent"},
	})
	if isErr {
		t.Fatalf("first create: %s", out)
	}
	firstID := decode(t, out)["resource"].(map[string]any)["id"].(string)

	out, isErr = callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource":  map[string]any{"type": "null_resource", "name": "qbittorrent"},
	})
	if !isErr {
		t.Fatalf("duplicate address accepted: %s", out)
	}
	// The refusal has to name the id, or the caller has no way to update.
	if !strings.Contains(out, firstID) {
		t.Errorf("refusal does not name the existing resource: %s", out)
	}
	p, _ := st.Get("p1")
	if len(p.Resources) != 1 {
		t.Fatalf("project holds %d resources with one address", len(p.Resources))
	}

	// Passing that id updates instead, which is the recovery the message asks for.
	if out, isErr = callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource":  map[string]any{"id": firstID, "type": "null_resource", "name": "qbittorrent"},
	}); isErr {
		t.Fatalf("update by id: %s", out)
	}

	// A data source shares neither namespace nor address with a resource.
	if out, isErr = callTool(t, cs, "upsert_resource", map[string]any{
		"projectId": "p1",
		"resource":  map[string]any{"type": "null_resource", "name": "qbittorrent", "kind": "data"},
	}); isErr {
		t.Errorf("data source blocked by a resource of the same name: %s", out)
	}
}

// TestMCPGetProjectSections: reading four locals must not cost the whole model.
func TestMCPGetProjectSections(t *testing.T) {
	cs, st := resourcesSession(t)
	if err := st.Update("p1", func(p *model.Project) error {
		p.Locals = []model.Local{{Name: "a", Value: model.AttrValue{Mode: model.ModeExpression, Expr: "1"}}}
		p.Resources = []model.Resource{{ID: "r1", Type: "null_resource", Name: "web"}}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, isErr := callTool(t, cs, "get_project", map[string]any{"projectId": "p1", "sections": "locals"})
	if isErr {
		t.Fatalf("get_project: %s", out)
	}
	doc := decode(t, out)
	if _, ok := doc["locals"]; !ok {
		t.Errorf("requested section missing: %s", out)
	}
	if _, ok := doc["resources"]; ok {
		t.Errorf("unrequested section returned: %s", out)
	}
	// Identity is always kept: a document you cannot tell the project of is worse
	// than a big one.
	if doc["id"] != "p1" || doc["name"] != "one" {
		t.Errorf("filtered document lost its identity: %s", out)
	}

	// A section that is empty is absent from the model document, so asking for
	// one must answer "none" rather than fail.
	if out, isErr = callTool(t, cs, "get_project", map[string]any{"projectId": "p1", "sections": "moved"}); isErr {
		t.Fatalf("empty section: %s", out)
	}
	if _, ok := decode(t, out)["moved"]; ok {
		t.Errorf("invented a moved list: %s", out)
	}
}
