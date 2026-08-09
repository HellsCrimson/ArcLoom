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
	ID        string
	Project   string
	Kind      string
	Env       string
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
	key, _ := keyFromContext(r)
	eff := effectiveUser(u, key)
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

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_project",
		Description: "Get a project's full model: providers, resources, data sources, variables, locals, outputs, modules and files. This is the source of truth the UI edits; use it before changing anything.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a projectArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" {
			return errResult("projectId is required"), nil, nil
		}
		return resultFor(s.callAPI(ctx, r, "GET", "/api/projects/"+url.PathEscape(a.ProjectID), nil))
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

	// get_operation is a read, but it only ever reports ops this server started,
	// so it is registered for everyone and filters by nothing else.
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
		return s.mcpOperationStatus(a.OperationID)
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
		Name:        "upsert_resource",
		Description: "Create or update a resource or data source. The whole resource is replaced, so always resend dependsOn, count, forEach and provider. Call get_resource_schema first to learn which attributes exist.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a upsertArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || len(a.Resource) == 0 {
			return errResult("projectId and resource are required"), nil, nil
		}
		return resultFor(s.callAPI(ctx, r,
			"POST", "/api/projects/"+url.PathEscape(a.ProjectID)+"/resources", mustJSON(a.Resource)))
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
		Config    map[string]any `json:"config" jsonschema:"object with variables, locals and outputs arrays; replaces all three"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "set_config",
		Description: "Replace a project's variables, locals and outputs. Send all three lists; anything omitted is cleared.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a configArgs) (*mcp.CallToolResult, any, error) {
		if a.ProjectID == "" || len(a.Config) == 0 {
			return errResult("projectId and config are required"), nil, nil
		}
		return resultFor(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/config", mustJSON(a.Config)))
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
		return resultFor(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/providers/"+url.PathEscape(a.Key), mustJSON(a.Config)))
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
		return resultFor(s.callAPI(ctx, r,
			"PUT", "/api/projects/"+url.PathEscape(a.ProjectID)+"/files", body))
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

	s.registerSchemaTools(srv, r)
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

// mcpOperationStatus reports a background op's progress.
func (s *Server) mcpOperationStatus(id string) (*mcp.CallToolResult, any, error) {
	s.mcpMu.Lock()
	op, ok := s.mcpOps[id]
	s.mcpMu.Unlock()
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
