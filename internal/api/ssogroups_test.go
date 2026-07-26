package api

import (
	"slices"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

func TestRolesForGroups(t *testing.T) {
	roles := []settings.GroupRole{
		{Group: "platform-admins", Admin: true},
		{Group: "developers", Perms: []string{user.PermResources, user.PermDeploy}},
		{Group: "release", Perms: []string{user.PermGit, user.PermDeploy}},
	}
	cases := []struct {
		name      string
		groups    []string
		wantAdmin bool
		wantPerms []string
	}{
		{"no groups stays read-only", nil, false, nil},
		{"unmatched group stays read-only", []string{"interns"}, false, nil},
		{"admin group", []string{"platform-admins"}, true, nil},
		{"perm group", []string{"developers"}, false, []string{user.PermResources, user.PermDeploy}},
		{
			// Two matching rows union, and a repeated perm appears once.
			"multiple groups union",
			[]string{"developers", "release"},
			false,
			[]string{user.PermResources, user.PermDeploy, user.PermGit},
		},
		{"admin wins over perms", []string{"developers", "platform-admins"}, true, nil},
		// Keycloak reports group paths; admins type the bare name.
		{"leading slash is ignored", []string{"/platform-admins"}, true, nil},
		{"case-insensitive", []string{"Platform-Admins"}, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			admin, perms := rolesForGroups(tc.groups, roles)
			if admin != tc.wantAdmin {
				t.Errorf("admin = %v, want %v", admin, tc.wantAdmin)
			}
			if !slices.Equal(perms, tc.wantPerms) {
				t.Errorf("perms = %v, want %v", perms, tc.wantPerms)
			}
		})
	}
}

func TestRolesForGroupsEmptyMappingGrantsNothing(t *testing.T) {
	// The pre-existing default: SSO users are read-only unless mapped.
	if admin, perms := rolesForGroups([]string{"anything"}, nil); admin || len(perms) > 0 {
		t.Fatalf("unmapped install must grant nothing, got admin=%v perms=%v", admin, perms)
	}
}

func TestMatchesGroupIgnoresBlankConfig(t *testing.T) {
	// A blank configured group must never match, or an unfilled row would grant
	// access to everyone.
	if matchesGroup([]string{"", "devs"}, "  ") {
		t.Fatal("a blank group name must not match")
	}
}

func TestGroupsFromClaim(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"json array", []any{"a", "b"}, []string{"a", "b"}},
		{"array with non-strings", []any{"a", 42, nil, "b"}, []string{"a", "b"}},
		{"array with blanks", []any{"a", "  ", "b"}, []string{"a", "b"}},
		{"single string", "solo", []string{"solo"}},
		{"comma separated", "a,b", []string{"a", "b"}},
		{"space separated", "a b", []string{"a", "b"}},
		{"missing claim", nil, nil},
		{"unexpected type", 42, nil},
		{"string slice", []string{"a"}, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.GroupsFromClaim(tc.in); !slices.Equal(got, tc.want) {
				t.Errorf("GroupsFromClaim(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeGroupRoles(t *testing.T) {
	in := []settings.GroupRole{
		{Group: "  admins  ", Admin: true},
		{Group: "", Perms: []string{user.PermGit}},                                   // unfilled row: dropped
		{Group: "devs", Perms: []string{user.PermResources, user.PermResources, ""}}, // deduped, blank dropped
	}
	out, err := normalizeGroupRoles(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 rows, got %+v", out)
	}
	if out[0].Group != "admins" || !out[0].Admin {
		t.Errorf("row 0 = %+v", out[0])
	}
	if !slices.Equal(out[1].Perms, []string{user.PermResources}) {
		t.Errorf("row 1 perms = %v", out[1].Perms)
	}
}

func TestNormalizeGroupRolesRejectsUnknownPerm(t *testing.T) {
	_, err := normalizeGroupRoles([]settings.GroupRole{{Group: "devs", Perms: []string{"root"}}})
	if err == nil {
		t.Fatal("an unknown permission must be rejected, not silently stored")
	}
}

func TestSSOScopesAddsGroupsOnlyWhenMapped(t *testing.T) {
	// No mapping: the request is unchanged from before this feature existed.
	if got := ssoScopes(settings.SSO{Scopes: "email profile"}); !slices.Equal(got, []string{"email", "profile"}) {
		t.Errorf("unmapped scopes = %v", got)
	}
	// Mapped: "groups" is requested, since most IdPs omit the claim otherwise.
	got := ssoScopes(settings.SSO{Scopes: "email", GroupRoles: []settings.GroupRole{{Group: "a", Admin: true}}})
	if !slices.Equal(got, []string{"email", "groups"}) {
		t.Errorf("mapped scopes = %v", got)
	}
	// Already listed by the admin: not duplicated.
	got = ssoScopes(settings.SSO{Scopes: "groups", GroupRoles: []settings.GroupRole{{Group: "a", Admin: true}}})
	if !slices.Equal(got, []string{"groups"}) {
		t.Errorf("duplicate scopes = %v", got)
	}
}
