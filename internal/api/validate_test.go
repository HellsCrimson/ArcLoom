package api

import (
	"net/http"
	"testing"
)

// A path-traversing {id} is rejected with 400 before it ever reaches a handler
// that would interpolate it into a filesystem path — and the check sits above
// the permission gates, so a forged id is refused even for a route the caller
// lacks permission on (400, not 403).
func TestValidateID_RejectsTraversal(t *testing.T) {
	h, _, _ := newRBACServer(t)
	editor := login(t, h, "editor", "password1") // has resources perm
	viewer := login(t, h, "viewer", "password1") // no perms

	// Dotted / traversal ids are refused on both read and write routes.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/projects/p1.x"},
		{http.MethodGet, "/api/projects/p1.x/hcl"},
		{http.MethodGet, "/api/projects/a.b/git/status"},
		{http.MethodGet, "/api/projects/..secret/state"},
	} {
		if rec := do(t, h, tc.method, tc.path, editor, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: got %d, want 400", tc.method, tc.path, rec.Code)
		}
	}

	// The id check runs before the permission check: a viewer hitting a
	// resources-gated route with a bad id gets 400, not 403.
	if rec := do(t, h, http.MethodPut, "/api/projects/p1.x/backend", viewer, `{"type":"local"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id on gated route: got %d, want 400 (before perm gate)", rec.Code)
	}
}

// A well-formed id passes the validator and reaches the handler (404 for a
// missing project, never 400).
func TestValidateID_AllowsWellFormed(t *testing.T) {
	h, _, _ := newRBACServer(t)
	viewer := login(t, h, "viewer", "password1")

	for _, id := range []string{"p1", "550e8400-e29b-41d4-a716-446655440000", "under_score"} {
		if rec := do(t, h, http.MethodGet, "/api/projects/"+id, viewer, ""); rec.Code == http.StatusBadRequest {
			t.Errorf("id %q rejected as invalid, want it to reach the handler", id)
		}
	}
}

// validProviderSource accepts real registry addresses and rejects anything that
// could carry path-traversal or HCL-identifier-breaking characters into
// run.ProvidersSchema / the required_providers key.
func TestValidProviderSource(t *testing.T) {
	valid := []string{
		"aws", "hashicorp/aws", "hashicorp/random", "hashicorp/google-beta",
		"registry.terraform.io/hashicorp/aws", "registry.opentofu.org/hashicorp/null",
	}
	for _, s := range valid {
		if !validProviderSource(s) {
			t.Errorf("validProviderSource(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"", "../../etc/passwd", "hashicorp/aws;rm", "a/b/c/d", "/aws",
		"hashicorp/", "1aws", "has hicorp/aws", "hashicorp/aws\n", "hashi\\corp/aws",
	}
	for _, s := range invalid {
		if validProviderSource(s) {
			t.Errorf("validProviderSource(%q) = true, want false", s)
		}
	}
}

// The provider-schema endpoints init + execute the named provider plugin, so
// they now require PermResources: a read-only viewer is blocked (403) before any
// binary runs, while an editor passes the gate and reaches the handler.
func TestSchemaEndpoints_RequireResourcesPerm(t *testing.T) {
	h, _, _ := newRBACServer(t)
	viewer := login(t, h, "viewer", "password1") // no perms
	editor := login(t, h, "editor", "password1") // resources perm

	paths := []string{
		"/api/providers/hashicorp%2Faws/schema?engine=tofu",
		"/api/providers/hashicorp%2Faws/resources/aws_instance/schema?engine=tofu",
		"/api/providers/hashicorp%2Faws/datasources/aws_ami/schema?engine=tofu",
	}
	for _, p := range paths {
		if rec := do(t, h, http.MethodGet, p, viewer, ""); rec.Code != http.StatusForbidden {
			t.Errorf("viewer GET %s: got %d, want 403", p, rec.Code)
		}
		// Editor clears the gate (no runner is configured, so the handler itself
		// answers 400 "unknown engine" — the point is it's never 403).
		if rec := do(t, h, http.MethodGet, p, editor, ""); rec.Code == http.StatusForbidden {
			t.Errorf("editor GET %s: got 403, want it to pass the perm gate", p)
		}
	}
}

// A malformed {source} is rejected with 400 before any provider runs, even for a
// permitted user.
func TestSchemaEndpoint_RejectsBadSource(t *testing.T) {
	h, _, _ := newRBACServer(t)
	editor := login(t, h, "editor", "password1")
	if rec := do(t, h, http.MethodGet, "/api/providers/..%2F..%2Fetc/schema?engine=tofu", editor, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad source: got %d, want 400", rec.Code)
	}
}
