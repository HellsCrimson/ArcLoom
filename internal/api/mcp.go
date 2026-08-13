package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// MCP (Model Context Protocol) endpoint — lets an AI agent drive ArcLoom.
//
// Transport is Streamable HTTP at /api/mcp, mounted inside the protected route
// group so requireAuth / validateID / limitBody already wrap it. That choice is
// client-facing: a remote HTTP server needs no install step (a stdio server is
// a subprocess the client launches), it works when ArcLoom is a homelab box and
// the agent is elsewhere, and it is the only shape claude.ai connectors and the
// API's MCP connector accept.
//
// Two rules shape everything below:
//
//  1. Tools re-enter the REST handlers (see mcpcall.go); nothing here talks to
//     the store, so nothing here can skip a validation.
//  2. tools/list is filtered by the caller's *effective* permissions, so an
//     agent is never shown a tool it would be 403'd on. The SDK's getServer is
//     a per-request factory, which is what makes that possible at all.
const (
	// mcpOpTimeout bounds a background engine run started by a tool call.
	mcpOpTimeout = 30 * time.Minute
	// mcpLogTail is how much of a running op's output get_operation returns.
	// The full log is in the run record; this is a progress indicator.
	mcpLogTail = 4000
	// mcpMaxOps bounds the in-memory op table.
	mcpMaxOps = 100
)

// mcpOp is a background engine run started by a tool call.
type mcpOp struct {
	ID      string
	Project string
	Kind    string
	Env     string
	// UserID is who started it. The status payload includes a tail of the
	// engine output, so an operation is readable only by its owner: the id is a
	// UUID and not guessable, but "unguessable" is not an authorization check,
	// and ids travel through agent transcripts and logs where they leak easily.
	UserID    string
	StartedAt time.Time
	w         *bufWriter
}

// mcpEnabled reports whether the MCP endpoint should be mounted. It needs the
// key store: without a credential a non-browser client cannot authenticate, so
// the endpoint would exist and refuse everything.
func (s *Server) mcpEnabled() bool { return s.cfg.MCPEnabled && s.apikeys != nil }

// mcpHandler builds the Streamable HTTP handler.
//
// Stateless is deliberate: every request re-authenticates from its own
// Authorization header, so there is no per-connection state worth keeping, and
// a stateless server cannot end up serving one caller's session to another.
func (s *Server) mcpHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return s.mcpServerFor(r) },
		&mcp.StreamableHTTPOptions{
			Stateless: true,
			// The protected group already caps bodies at maxBodyLarge; match it
			// rather than inheriting the SDK's smaller default, which would
			// truncate import_hcl — the one tool with a large payload — while
			// every other tool looked fine.
			MaxRequestBodyBytes: maxBodyLarge,
		},
	)
}

// mcpServerFor builds the MCP server for one request, registering only the
// tools the caller may actually use.
func (s *Server) mcpServerFor(r *http.Request) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "arcloom",
		Title:   "ArcLoom",
		Version: s.build.Version,
	}, &mcp.ServerOptions{
		// Do NOT advertise tools.listChanged. The SDK infers `{listChanged:true}`
		// as soon as a tool is registered, but a Stateless server has no channel
		// to deliver `notifications/tools/list_changed` on — there is no standalone
		// SSE stream, GET is 405 by design. Claiming it invites a client to trust a
		// notification that can never arrive and therefore never re-poll
		// `tools/list`; the honest signal is to say we cannot, so a client that
		// cares re-lists on its own terms.
		//
		// Practical consequence either way: a server upgrade that adds a tool is
		// picked up when the client reconnects, not before.
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})

	// Effective capability: the user intersected with the API key's scopes (see
	// effectiveUser). This is the *same* intersection the middleware enforces,
	// so the advertised tool set and the enforced one cannot drift.
	u, _ := userFromContext(r)
	cred, _ := credentialFromContext(r)
	eff := effectiveUser(u, cred)
	can := func(perm string) bool {
		if !s.cfg.AuthEnabled || eff == nil {
			return true // auth off: the app is open
		}
		return eff.Can(perm)
	}

	s.registerReadTools(srv, r)
	if can(user.PermResources) {
		s.registerWriteTools(srv, r)
	}
	if can(user.PermDeploy) {
		s.registerDeployTools(srv, r)
	}
	return srv
}

// --- tool result helpers ---

// textResult wraps a string as a tool result.
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// errResult reports a tool failure to the model (IsError, not a Go error, so
// the agent sees the message and can adapt).
func errResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}

// resultFor turns a re-entrant API response into a tool result. A non-2xx is
// surfaced as a tool error carrying the handler's own message — which is the
// whole benefit of re-entering: the agent gets ArcLoom's real validation error
// ("count and for_each are mutually exclusive") rather than a generic failure.
func resultFor(res apiResult) (*mcp.CallToolResult, any, error) {
	if !res.ok() {
		return errResult("ArcLoom returned %d: %s", res.Status, strings.TrimSpace(res.Body)), nil, nil
	}
	body := res.Body
	if body == "" {
		body = "OK"
	}
	return textResult(body), nil, nil
}

// mustJSON marshals a tool argument for the re-entrant call. The argument was
// already decoded from the client's JSON, so it cannot fail to re-encode.
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ackResult answers a write whose handler returned the whole project with a
// compact acknowledgement: what changed, what the project holds now, and any
// dangling references the write left behind (see modelWarnings).
//
// The full document is deliberately dropped. It is the SPA's contract, not a
// useful tool result — six figures of characters an agent already knows, often
// past the client's per-result limit, on every single write.
func ackResult(res apiResult, action string) (*mcp.CallToolResult, any, error) {
	if !res.ok() {
		return resultFor(res)
	}
	p, err := projectFromBody(res.Body)
	if err != nil {
		// The write succeeded — say so rather than failing the tool call, but do
		// not fall back to echoing a body this size.
		return textResult(fmt.Sprintf(`{"ok":true,"action":%q,"note":"write succeeded; response could not be summarized"}`, action)), nil, nil
	}
	out := map[string]any{
		"ok":        true,
		"action":    action,
		"projectId": p.ID,
		"counts":    projectCounts(p),
	}
	if w := modelWarnings(p); len(w) > 0 {
		out["warnings"] = w
	}
	return textResult(string(mustJSON(out))), nil, nil
}

// withWarnings attaches model warnings to a write whose own response is already
// small enough to return verbatim (upsert_resource answers with one resource).
func (s *Server) withWarnings(ctx context.Context, r *http.Request, projectID string, res apiResult, key string) (*mcp.CallToolResult, any, error) {
	if !res.ok() {
		return resultFor(res)
	}
	out := map[string]any{"ok": true}
	if key != "" && res.Body != "" {
		out[key] = json.RawMessage(res.Body)
	}
	if w := s.warningsFor(ctx, r, projectID); len(w) > 0 {
		out["warnings"] = w
	}
	return textResult(string(mustJSON(out))), nil, nil
}

// --- read tools (no scope required) ---

type projectArgs struct {
	ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
}

type moduleArgs struct {
	ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
	Module    string `json:"module,omitempty" jsonschema:"optional local module name; omit for the root module"`
}

type envArgs struct {
	ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
	Env       string `json:"env,omitempty" jsonschema:"optional environment name; omit for the default environment"`
}

func (s *Server) registerReadTools(srv *mcp.Server, r *http.Request) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_projects",
		Description: "List the ArcLoom projects this credential can see, with resource counts and drift status. Start here to find a project id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return resultFor(s.callAPI(ctx, r, "GET", "/api/projects", nil))
	})

	type getProjectArgs struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		Sections  string `json:"sections,omitempty" jsonschema:"comma-separated top-level keys to return, e.g. \"locals,resources\"; omit for the whole model. A big project runs to six figures of characters, so ask for what you need."`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_project",
		Description: "Get a project's model: providers, resources, data sources, variables, locals, outputs, modules, files, and any import/moved blocks. This is the source of truth the UI edits; use it before changing anything. " +
			"An empty list is omitted rather than sent as [], so a missing key means none — not that the section is unreadable. " +
			"Note that the variables in the generated variables.tf are not all here: a secret reference auto-declares one at generation time, and those are not part of the model.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a getProjectArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		res := s.callAPI(ctx, r, "GET", "/api/projects/"+url.PathEscape(a.ProjectID), nil)
		if !res.ok() || a.Sections == "" {
			return resultFor(res)
		}
		filtered, err := filterSections(res.Body, a.Sections)
		if err != nil {
			return resultFor(res) // fall back to the whole model rather than failing
		}
		return textResult(filtered), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_hcl",
		Description: "Get the generated Terraform/OpenTofu HCL for a project as a map of filename to contents. Generated config is the output, not the input: to change it, edit the model with the write tools.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a moduleArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		path := "/api/projects/" + url.PathEscape(a.ProjectID) + "/hcl"
		if a.Module != "" {
			path += "?module=" + url.QueryEscape(a.Module)
		}
		return resultFor(s.callAPI(ctx, r, "GET", path, nil))
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_state",
		Description: "List the resources tracked in a project's Terraform state, grouped by module, with each instance's real address. This is the only place that knows what a count/for_each actually expanded to.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a envArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		return resultFor(s.callAPI(ctx, r, "GET", withEnv("/api/projects/"+url.PathEscape(a.ProjectID)+"/state", a.Env), nil))
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_runs",
		Description: "List recent runs (plan, apply, destroy, drift, lint, state ops) for a project, newest first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a projectArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		return resultFor(s.callAPI(ctx, r, "GET", "/api/projects/"+url.PathEscape(a.ProjectID)+"/runs", nil))
	})

	type runArgs struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		RunID     string `json:"runId" jsonschema:"the run id from list_runs"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_run",
		Description: "Get one run in full: the streamed log, the per-resource plan changes with attribute diffs, cost and policy results, and any outputs.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a runArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.RunID == "" {
			return errResult("projectId and runId are required"), nil, nil
		}
		return resultFor(s.callAPI(ctx, r,
			"GET", "/api/projects/"+url.PathEscape(a.ProjectID)+"/runs/"+url.PathEscape(a.RunID), nil))
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_drift",
		Description: "Get a project's drift-detection schedule and the latest check outcome, including which resource addresses drifted.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a projectArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		return resultFor(s.callAPI(ctx, r, "GET", "/api/projects/"+url.PathEscape(a.ProjectID)+"/drift", nil))
	})

	// get_operation is a read, so it is registered for everyone — but its payload
	// includes the tail of the engine output, so it is scoped to the operations
	// the CALLER started (see mcpOperationStatus).
	type opArgs struct {
		OperationID string `json:"operationId" jsonschema:"the id returned by plan/apply/validate/lint"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_operation",
		Description: "Poll a long-running operation started by plan, apply, validate or lint. Returns whether it is still running plus the tail of its output; once finished, use list_runs/get_run for the full record.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a opArgs) (*mcp.CallToolResult, any, error) {
		if a.OperationID == "" {
			return errResult("operationId is required"), nil, nil
		}
		return s.mcpOperationStatus(r, a.OperationID)
	})
}

// withEnv appends the ?env= selector when set.
func withEnv(path, env string) string {
	if env == "" {
		return path
	}
	return path + "?env=" + url.QueryEscape(env)
}

// --- write tools (resources scope) ---

func (s *Server) registerWriteTools(srv *mcp.Server, r *http.Request) {
	type upsertArgs struct {
		ProjectID string         `json:"projectId" jsonschema:"the ArcLoom project id"`
		Resource  map[string]any `json:"resource" jsonschema:"the resource object: id (omit to create) type name attrs dependsOn count forEach provider kind module. kind is \"resource\" (default) or \"data\"."`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "upsert_resource",
		Description: "Create or update a resource or data source. The whole resource is replaced, so always resend dependsOn, count, forEach and provider. Call get_resource_schema first to learn which attributes exist. " +
			"The id is the update key: omit it to create, and pass the existing id to change one. Creating a second resource with a type and name another already holds is refused with that resource's id, because the two would generate the same HCL block.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a upsertArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || len(a.Resource) == 0 {
			return errResult("projectId and resource are required"), nil, nil
		}
		res := s.callAPI(ctx, r,
			"POST", "/api/projects/"+url.PathEscape(a.ProjectID)+"/resources", mustJSON(a.Resource))
		return s.withWarnings(ctx, r, a.ProjectID, res, "resource")
	})

	type deleteResourceArgs struct {
		ProjectID  string `json:"projectId" jsonschema:"the ArcLoom project id"`
		ResourceID string `json:"resourceId" jsonschema:"the resource's id from get_project"`
		Kind       string `json:"kind,omitempty" jsonschema:"\"resource\" (default) or \"data\""`
		Module     string `json:"module,omitempty" jsonschema:"local module name; omit for the root module"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_resource",
		Description: "Delete a resource or data source from the project model. This removes it from the config; run a plan to see what that would destroy.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a deleteResourceArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.ResourceID == "" {
			return errResult("projectId and resourceId are required"), nil, nil
		}
		q := url.Values{}
		if a.Kind != "" {
			q.Set("kind", a.Kind)
		}
		if a.Module != "" {
			q.Set("module", a.Module)
		}
		path := "/api/projects/" + url.PathEscape(a.ProjectID) + "/resources/" + url.PathEscape(a.ResourceID)
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		return resultFor(s.callAPI(ctx, r, "DELETE", path, nil))
	})

	type configArgs struct {
		ProjectID string         `json:"projectId" jsonschema:"the ArcLoom project id"`
		Config    map[string]any `json:"config" jsonschema:"object with variables, locals and outputs arrays, plus an optional module name to target a local module instead of the root"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_config",
		Description: "Replace a project's variables, locals and outputs. Each of the three lists you send replaces the stored one, and a list you omit is cleared — so read them with get_project first (sections: \"variables,locals,outputs\") and send back the full lists. " +
			"This never touches the variables hclgen auto-declares for secret references; those are derived at generation time and are not part of the config. Pass module to edit a local module's config instead of the root's.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a configArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || len(a.Config) == 0 {
			return errResult("projectId and config are required"), nil, nil
		}
		return ackResult(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/config", mustJSON(a.Config)), "set_config")
	})

	type providerArgs struct {
		ProjectID string         `json:"projectId" jsonschema:"the ArcLoom project id"`
		Key       string         `json:"key" jsonschema:"the provider's local name, e.g. \"aws\""`
		Config    map[string]any `json:"config" jsonschema:"object with source, version, alias and values"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "set_provider",
		Description: "Configure a provider instance (source, version constraint, alias and argument values). Omitting version leaves the pin alone; an empty string unpins.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a providerArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.Key == "" || len(a.Config) == 0 {
			return errResult("projectId, key and config are required"), nil, nil
		}
		return ackResult(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/providers/"+url.PathEscape(a.Key), mustJSON(a.Config)), "set_provider")
	})

	type fileArgs struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		Path      string `json:"path" jsonschema:"path relative to the root module, e.g. templates/cloud-init.tftpl"`
		Content   string `json:"content" jsonschema:"the file's text content"`
		OldPath   string `json:"oldPath,omitempty" jsonschema:"previous path, to rename in one call"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "set_file",
		Description: "Create, update or rename a companion file (cloud-init templates, scripts, JSON policy documents) that HCL reaches via file() or templatefile(). Cannot write .tf files — HCL comes from the model.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a fileArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.Path == "" {
			return errResult("projectId and path are required"), nil, nil
		}
		body, _ := json.Marshal(map[string]string{"path": a.Path, "content": a.Content, "oldPath": a.OldPath})
		return ackResult(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/files", body), "set_file")
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_file",
		Description: "Delete a companion file from the project. Only companion files — generated .tf files are not stored, they are rendered from the model.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		Path      string `json:"path" jsonschema:"the file's path, as listed in the project's files"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.Path == "" {
			return errResult("projectId and path are required"), nil, nil
		}
		return ackResult(s.callAPI(ctx, r, "DELETE",
			"/api/projects/"+url.PathEscape(a.ProjectID)+"/files?path="+url.QueryEscape(a.Path), nil), "delete_file")
	})

	type importArgs struct {
		ProjectID string            `json:"projectId" jsonschema:"the ArcLoom project id"`
		Files     map[string]string `json:"files" jsonschema:"map of file path to HCL contents; use real paths like main.tf or modules/team/main.tf"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "import_hcl",
		Description: "Import existing Terraform/OpenTofu HCL into the project model. Merges into what is already there, renaming colliding resources and reporting anything it had to skip.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a importArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || len(a.Files) == 0 {
			return errResult("projectId and files are required"), nil, nil
		}
		body, _ := json.Marshal(map[string]any{"files": a.Files})
		return resultFor(s.callAPI(ctx, r,
			"POST", "/api/projects/"+url.PathEscape(a.ProjectID)+"/import/hcl", body))
	})

	s.registerRefactorTools(srv, r)
	s.registerModuleTools(srv, r)
	s.registerSchemaTools(srv, r)
}

// registerRefactorTools exposes the two config-driven state operations.
//
// They are what makes an address-changing refactor expressible at all. Folding
// hand-written resources into one for_each'd block renames every address, and
// an apply that meets a renamed address destroys the real object and creates a
// new one — so a refactor without these tools is not a slower path, it is a
// destructive one. The engine's own answer is a `moved` block; ArcLoom models
// them, and the UI has always been able to write them.
func (s *Server) registerRefactorTools(srv *mcp.Server, r *http.Request) {
	type movedBlock struct {
		From string `json:"from" jsonschema:"the address as the state holds it now, e.g. aws_instance.web"`
		To   string `json:"to" jsonschema:"the address the config declares now, e.g. aws_instance.main[\"web\"]"`
		ID   string `json:"id,omitempty" jsonschema:"an existing block's id, from get_project; omit for a new one"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_moved",
		Description: "Replace the project's `moved` blocks — the config-driven way to rename resources without destroying them. " +
			"Write one per renamed address BEFORE applying a refactor that changes addresses (folding several resources into one for_each, renaming a resource, moving one into a module); the next plan then lists the renames and the apply performs them, in one run. " +
			"Both fields are static addresses, so an index must be a literal: `x.y[\"web\"]` is fine, `x.y[each.key]` is not. " +
			"This replaces the whole list, so send every block you want to keep. They are never removed automatically — delete them (send the list without them) once the apply that used them has run.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string       `json:"projectId" jsonschema:"the ArcLoom project id"`
		Moved     []movedBlock `json:"moved" jsonschema:"the complete list of moved blocks; an empty list clears them"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		body, _ := json.Marshal(map[string]any{"moved": a.Moved})
		return ackResult(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/moved", body), "set_moved")
	})

	type importBlock struct {
		To       string `json:"to" jsonschema:"the address to adopt into, e.g. aws_instance.main[each.key]"`
		ID       string `json:"id" jsonschema:"the existing object's id in the provider's terms; may be an expression such as each.value"`
		ForEach  string `json:"forEach,omitempty" jsonschema:"optional for_each expression, to adopt a whole collection in one block"`
		Provider string `json:"provider,omitempty" jsonschema:"optional provider reference, \"<key>\" or \"<key>.<alias>\""`
		BlockID  string `json:"blockId,omitempty" jsonschema:"an existing block's id, from get_project; omit for a new one"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_imports",
		Description: "Replace the project's `import` blocks — how existing infrastructure is adopted into state by the next apply. " +
			"Unlike a CLI import, the id may be an expression, so a for_each block adopts a whole collection in one pass. " +
			"Replaces the whole list; blocks are never removed automatically, and one whose target is already in state is a no-op. Needs engine >= 1.5, or >= 1.7 to use forEach.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string        `json:"projectId" jsonschema:"the ArcLoom project id"`
		Imports   []importBlock `json:"imports" jsonschema:"the complete list of import blocks; an empty list clears them"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		list := make([]map[string]string, 0, len(a.Imports))
		for _, imp := range a.Imports {
			list = append(list, map[string]string{
				"id": imp.BlockID, "to": imp.To, "importId": imp.ID,
				"forEach": imp.ForEach, "provider": imp.Provider,
			})
		}
		body, _ := json.Marshal(map[string]any{"imports": list})
		return ackResult(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/imports", body), "set_imports")
	})
}

// registerModuleTools exposes local module definitions and module calls.
//
// A call and a definition are different things and both are needed: the
// definition is the folder (its own variables, resources and outputs, edited
// with set_config / upsert_resource passing module), the call is the
// `module "name"` block that uses it. One definition can back several calls.
func (s *Server) registerModuleTools(srv *mcp.Server, r *http.Request) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_local_module",
		Description: "Create a local module definition — a modules/<name>/ folder of its own variables, resources and outputs. " +
			"By default it also adds a call of the same name; pass call:false to define one without using it yet. Edit its contents with set_config and upsert_resource, passing module:\"<name>\".",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		Name      string `json:"name" jsonschema:"the module name: letters, digits, _ and -, starting with a letter or _"`
		Call      *bool  `json:"call,omitempty" jsonschema:"also add a module call using it (default true)"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.Name == "" {
			return errResult("projectId and name are required"), nil, nil
		}
		body := map[string]any{"name": a.Name}
		if a.Call != nil {
			body["call"] = *a.Call
		}
		return ackResult(s.callAPI(ctx, r,
			"POST", "/api/projects/"+url.PathEscape(a.ProjectID)+"/modules/local", mustJSON(body)), "create_local_module")
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_module_call",
		Description: "Create or update a module call — the `module \"<name>\"` block, its source, its input values and its count/for_each. " +
			"Works for a local source (\"./modules/team\", which must already be defined) and a registry one alike. The whole call is replaced, so resend count, forEach and every input you want to keep; pass rename to change its name.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string         `json:"projectId" jsonschema:"the ArcLoom project id"`
		Call      map[string]any `json:"call" jsonschema:"the call: name, source, optional rename, version, config (input values as attribute objects), count, forEach, providers"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || len(a.Call) == 0 {
			return errResult("projectId and call are required"), nil, nil
		}
		return ackResult(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/modules/call", mustJSON(a.Call)), "set_module_call")
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_module_call",
		Description: "Delete a module call by name. The local module definition it used stays, along with any other call of it; run a plan to see what dropping the call would destroy.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		Name      string `json:"name" jsonschema:"the call's name, as it appears in the project's modules"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.Name == "" {
			return errResult("projectId and name are required"), nil, nil
		}
		return ackResult(s.callAPI(ctx, r, "DELETE",
			"/api/projects/"+url.PathEscape(a.ProjectID)+"/modules/call/"+url.PathEscape(a.Name), nil), "delete_module_call")
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "delete_local_module",
		Description: "Delete a local module definition and everything in it. Refused while a call still uses it, naming the calls; force:true deletes those calls too.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		Name      string `json:"name" jsonschema:"the definition's name, as it appears in the project's localModules"`
		Force     bool   `json:"force,omitempty" jsonschema:"also delete the calls that use it"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.Name == "" {
			return errResult("projectId and name are required"), nil, nil
		}
		path := "/api/projects/" + url.PathEscape(a.ProjectID) + "/modules/local/" + url.PathEscape(a.Name)
		if a.Force {
			path += "?force=true"
		}
		return ackResult(s.callAPI(ctx, r, "DELETE", path, nil), "delete_local_module")
	})
}

// registerSchemaTools exposes provider schema discovery — how an agent learns
// which attributes a resource type actually has.
//
// These live behind the resources scope, not the read tier, because they init
// and execute the named provider plugin: a read-only credential must not be
// able to make the server download and run an arbitrary binary. That is the
// same reason the underlying routes sit in the resources group.
func (s *Server) registerSchemaTools(srv *mcp.Server, r *http.Request) {
	type typesArgs struct {
		Source  string `json:"source" jsonschema:"provider source, e.g. \"hashicorp/aws\" or \"registry.opentofu.org/hashicorp/null\""`
		Version string `json:"version,omitempty" jsonschema:"optional version constraint; omit for latest"`
		Filter  string `json:"filter,omitempty" jsonschema:"case-insensitive substring to narrow the list, e.g. \"instance\""`
		Limit   int    `json:"limit,omitempty" jsonschema:"maximum names to return (default 200)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_resource_types",
		Description: "List the resource and data source type names a provider offers. Always pass a filter for a large provider — AWS alone has well over a thousand types.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a typesArgs) (*mcp.CallToolResult, any, error) {
		if a.Source == "" {
			return errResult("source is required"), nil, nil
		}
		// The type lists come from POST /providers (handleAddProvider), which
		// fetches and caches the whole provider schema and returns the names.
		// NOT from GET /providers/{source}/schema — despite the name, that one
		// returns the provider's own *config* schema (the arguments of the
		// `provider` block), which decodes into this struct as all-zero and
		// would silently report a provider with no resource types at all.
		body, _ := json.Marshal(map[string]string{"source": a.Source, "version": a.Version})
		res := s.callAPI(ctx, r, "POST", "/api/providers", body)
		if !res.ok() {
			return resultFor(res)
		}
		doc, err := parseProviderTypes(res.Body)
		if err != nil {
			return errResult("%v", err), nil, nil
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 200
		}
		resources, rTrunc := filterNames(doc.ResourceTypes, a.Filter, limit)
		data, dTrunc := filterNames(doc.DataSourceTypes, a.Filter, limit)
		out, _ := json.Marshal(map[string]any{
			"provider":             doc.Provider,
			"resourceTypes":        resources,
			"dataSourceTypes":      data,
			"resourcesTruncated":   rTrunc,
			"dataSourcesTruncated": dTrunc,
		})
		return textResult(string(out)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_provider_schema",
		Description: "Get the schema of a provider's own configuration block — the arguments you pass to set_provider (region, endpoint, credentials). This is not the list of resource types; use list_resource_types for that.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a struct {
		Source  string `json:"source" jsonschema:"provider source, e.g. \"hashicorp/aws\""`
		Version string `json:"version,omitempty" jsonschema:"optional version constraint; omit for latest"`
	}) (*mcp.CallToolResult, any, error) {
		if a.Source == "" {
			return errResult("source is required"), nil, nil
		}
		path := "/api/providers/" + url.PathEscape(a.Source) + "/schema"
		if a.Version != "" {
			path += "?version=" + url.QueryEscape(a.Version)
		}
		return resultFor(s.callAPI(ctx, r, "GET", path, nil))
	})

	type schemaArgs struct {
		Source     string `json:"source" jsonschema:"provider source, e.g. \"hashicorp/aws\""`
		Type       string `json:"type" jsonschema:"the resource or data source type, e.g. \"aws_instance\""`
		Kind       string `json:"kind,omitempty" jsonschema:"\"resource\" (default) or \"data\""`
		Version    string `json:"version,omitempty" jsonschema:"optional version constraint; omit for latest"`
		Attributes string `json:"attributes,omitempty" jsonschema:"comma-separated top-level field names to return; omit for the whole schema"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_resource_schema",
		Description: "Get the normalized schema for one resource or data source type: its fields, types, whether each is required, and nested blocks. Use the attributes filter to fetch only the fields you need — a full schema for a large type is big.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a schemaArgs) (*mcp.CallToolResult, any, error) {
		if a.Source == "" || a.Type == "" {
			return errResult("source and type are required"), nil, nil
		}
		seg := "resources"
		if a.Kind == "data" {
			seg = "datasources"
		}
		path := "/api/providers/" + url.PathEscape(a.Source) + "/" + seg + "/" + url.PathEscape(a.Type) + "/schema"
		if a.Version != "" {
			path += "?version=" + url.QueryEscape(a.Version)
		}
		res := s.callAPI(ctx, r, "GET", path, nil)
		if !res.ok() || a.Attributes == "" {
			return resultFor(res)
		}
		filtered, err := filterSchemaFields(res.Body, a.Attributes)
		if err != nil {
			return resultFor(res) // fall back to the whole schema rather than failing
		}
		return textResult(filtered), nil, nil
	})
}

// providerTypes is the type listing returned by POST /providers.
type providerTypes struct {
	Provider        string   `json:"provider"`
	ResourceTypes   []string `json:"resourceTypes"`
	DataSourceTypes []string `json:"dataSourceTypes"`
}

// parseProviderTypes decodes a type listing, **refusing a document that has no
// resourceTypes key at all** rather than reporting an empty provider.
//
// That distinction is the whole point: pointing this at the wrong endpoint
// (GET /providers/{source}/schema, the provider's own config schema) produced a
// perfectly valid JSON document that decoded to zero values, so a provider with
// a thousand resource types came back as "none" with no error anywhere. A
// missing key is a wiring bug and must be loud; a genuinely empty list stays
// legal.
func parseProviderTypes(body string) (providerTypes, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return providerTypes{}, fmt.Errorf("could not parse provider types: %w", err)
	}
	if _, ok := raw["resourceTypes"]; !ok {
		return providerTypes{}, errors.New("response carries no resourceTypes (wrong endpoint?)")
	}
	var doc providerTypes
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return providerTypes{}, fmt.Errorf("could not parse provider types: %w", err)
	}
	return doc, nil
}

// filterNames narrows a name list by substring and caps it, reporting whether
// anything was dropped — a silent truncation would read as "that's everything".
func filterNames(names []string, filter string, limit int) ([]string, bool) {
	out := []string{}
	needle := strings.ToLower(filter)
	total := 0
	for _, n := range names {
		if needle != "" && !strings.Contains(strings.ToLower(n), needle) {
			continue
		}
		total++
		if len(out) < limit {
			out = append(out, n)
		}
	}
	return out, total > len(out)
}

// filterSections keeps only the named top-level keys of a project document,
// plus the identity fields, so an agent can read the four locals it is about to
// rewrite without pulling the resources beside them.
//
// Unknown names are not an error: a section that is empty is absent from the
// document (omitempty), so "give me the moved blocks" of a project with none
// must answer "none" rather than refuse.
func filterSections(body, sections string) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return "", err
	}
	keep := map[string]bool{"id": true, "name": true, "engine": true}
	for _, n := range strings.Split(sections, ",") {
		if n = strings.TrimSpace(n); n != "" {
			keep[n] = true
		}
	}
	out := map[string]json.RawMessage{}
	for k, v := range doc {
		if keep[k] {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// filterSchemaFields keeps only the named top-level fields of a normalized
// schema document, so an agent can ask for three attributes of a type that has
// two hundred.
func filterSchemaFields(body, attributes string) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return "", err
	}
	raw, ok := doc["fields"].([]any)
	if !ok {
		return "", errors.New("no fields array")
	}
	want := map[string]bool{}
	for _, n := range strings.Split(attributes, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	kept := []any{}
	for _, f := range raw {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := m["name"].(string); want[name] {
			kept = append(kept, f)
		}
	}
	doc["fields"] = kept
	doc["fieldsFiltered"] = true
	out, err := json.Marshal(doc)
	return string(out), err
}

// --- deploy tools (deploy scope) ---

func (s *Server) registerDeployTools(srv *mcp.Server, r *http.Request) {
	stream := func(kind, method, suffix string) func(context.Context, *mcp.CallToolRequest, envArgs) (*mcp.CallToolResult, any, error) {
		return func(_ context.Context, _ *mcp.CallToolRequest, a envArgs) (*mcp.CallToolResult, any, error) {
			if a.ProjectID == "" {
				return errResult("projectId is required"), nil, nil
			}
			path := withEnv("/api/projects/"+url.PathEscape(a.ProjectID)+suffix, a.Env)
			return s.startMCPOp(r, kind, a.ProjectID, a.Env, method, path, nil)
		}
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "plan",
		Description: "Start a plan. Returns an operationId immediately — poll it with get_operation, then read the per-resource changes with get_run. A plan is safe: it never changes infrastructure.",
	}, stream("plan", "GET", "/plan"))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "validate",
		Description: "Start a validate (a syntax and consistency check that touches no infrastructure and needs no credentials). Returns an operationId; poll with get_operation.",
	}, stream("validate", "GET", "/validate"))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "lint",
		Description: "Start a tflint run over the generated config. Returns an operationId; poll with get_operation. Degrades to unavailable if tflint is not installed on the server.",
	}, stream("lint", "GET", "/lint"))

	mcp.AddTool(srv, &mcp.Tool{
		Name: "apply",
		Description: "Apply the pending changes to real infrastructure. This is not reversible. " +
			"Run plan first and read the result. If the project has the approval gate enabled, a human must approve that plan before this will run — that is the intended way to use this tool.",
	}, stream("apply", "POST", "/apply"))

	mcp.AddTool(srv, &mcp.Tool{
		Name: "move_state",
		Description: "Move one address in the Terraform state (`state mv`), renaming what the state tracks without touching infrastructure. " +
			"Prefer set_moved for a refactor: a moved block is part of the config, the plan lists every rename and one apply performs them all, while this runs one engine operation per address and each takes the workspace lock. " +
			"Use this when there is no config to attach a block to. Pass dryRun:true first — only the engine can say whether the move is legal. Returns an operationId; poll with get_operation.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a struct {
		ProjectID string `json:"projectId" jsonschema:"the ArcLoom project id"`
		From      string `json:"from" jsonschema:"the current address, e.g. aws_instance.web"`
		To        string `json:"to" jsonschema:"the new address, e.g. aws_instance.main[\"web\"]"`
		Env       string `json:"env,omitempty" jsonschema:"optional environment name; omit for the default environment"`
		DryRun    bool   `json:"dryRun,omitempty" jsonschema:"ask the engine what it would move, changing nothing"`
	}) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || a.From == "" || a.To == "" {
			return errResult("projectId, from and to are required"), nil, nil
		}
		q := url.Values{"from": {a.From}, "to": {a.To}}
		if a.Env != "" {
			q.Set("env", a.Env)
		}
		if a.DryRun {
			q.Set("dryRun", "true")
		}
		path := "/api/projects/" + url.PathEscape(a.ProjectID) + "/state/mv?" + q.Encode()
		return s.startMCPOp(r, "state-mv", a.ProjectID, a.Env, "POST", path, nil)
	})

	// Deliberately no destroy tool. The UI makes you type the project name to
	// destroy, which is a confirmation an agent can trivially satisfy and a
	// human cannot un-satisfy. Destroying everything is not something that
	// should be reachable by a model deciding it is the right next step; it
	// stays a deliberate human action in the UI.
}

// startMCPOp launches a streaming route in the background and returns its id.
func (s *Server) startMCPOp(r *http.Request, kind, projectID, env, method, path string, body []byte) (*mcp.CallToolResult, any, error) {
	w, _ := s.startAPICall(r, method, path, body, mcpOpTimeout)
	op := &mcpOp{
		ID:        uuid.NewString(),
		Project:   projectID,
		Kind:      kind,
		Env:       env,
		UserID:    userIDFor(r),
		StartedAt: time.Now(),
		w:         w,
	}
	s.mcpMu.Lock()
	if len(s.mcpOps) >= mcpMaxOps {
		// Drop the oldest finished op so a long-lived server cannot grow this
		// table without bound.
		var oldestID string
		var oldest time.Time
		for id, o := range s.mcpOps {
			if o.w.finished() && (oldestID == "" || o.StartedAt.Before(oldest)) {
				oldestID, oldest = id, o.StartedAt
			}
		}
		if oldestID != "" {
			delete(s.mcpOps, oldestID)
		}
	}
	s.mcpOps[op.ID] = op
	s.mcpMu.Unlock()

	out, _ := json.Marshal(map[string]any{
		"operationId": op.ID,
		"kind":        kind,
		"projectId":   projectID,
		"status":      "running",
		"next":        "poll get_operation with this operationId",
	})
	return textResult(string(out)), nil, nil
}

// userIDFor returns the authenticated user's id, or "" when auth is disabled
// (in which case there is one implicit user and nothing to separate).
func userIDFor(r *http.Request) string {
	if u, ok := userFromContext(r); ok && u != nil {
		return u.ID
	}
	return ""
}

// mcpOperationStatus reports a background op's progress. r identifies the
// caller, whose ownership of the operation is checked before any of its output
// is returned.
func (s *Server) mcpOperationStatus(r *http.Request, id string) (*mcp.CallToolResult, any, error) {
	s.mcpMu.Lock()
	op, ok := s.mcpOps[id]
	s.mcpMu.Unlock()
	// An operation belonging to someone else is reported as unknown rather than
	// forbidden: the caller has no business learning that the id exists.
	if ok && op.UserID != userIDFor(r) {
		ok = false
	}
	if !ok {
		return errResult("unknown operationId %q (operations do not survive a server restart; use list_runs instead)", id), nil, nil
	}
	status, body := op.w.snapshot()
	done := op.w.finished()
	tail := body
	if len(tail) > mcpLogTail {
		tail = "…" + tail[len(tail)-mcpLogTail:]
	}
	state := "running"
	if done {
		state = "finished"
	}
	out, _ := json.Marshal(map[string]any{
		"operationId": id,
		"kind":        op.Kind,
		"projectId":   op.Project,
		"status":      state,
		"httpStatus":  status,
		"elapsed":     time.Since(op.StartedAt).Round(time.Second).String(),
		"output":      tail,
		"next": map[string]string{
			"running":  "poll again in a few seconds",
			"finished": "call list_runs then get_run for the structured result",
		}[state],
	})
	return textResult(string(out)), nil, nil
}
