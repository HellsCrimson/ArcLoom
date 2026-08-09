package apikey

import (
	"path/filepath"
	"testing"
	"time"
)

// stores returns every Store implementation so the same behavioural contract is
// asserted against both (the disk one is what production uses; the memory one
// is what the API tests use, and a divergence between them would make those
// tests lie).
func stores(t *testing.T) map[string]Store {
	t.Helper()
	disk, err := NewDisk(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatalf("NewDisk: %v", err)
	}
	return map[string]Store{"memory": NewMemory(), "disk": disk}
}

func TestCreateAndVerify(t *testing.T) {
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			token, err := st.Create(&Key{UserID: "u1", Label: "ci", Scopes: []string{"resources"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			id, secret, ok := ParseToken(token)
			if !ok {
				t.Fatalf("ParseToken(%q) failed", token)
			}
			if secret == "" {
				t.Fatal("empty secret half")
			}
			k, err := st.Verify(token)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if k.ID != id || k.UserID != "u1" || !k.HasScope("resources") {
				t.Fatalf("verified key mismatch: %+v", k)
			}
			// The secret must not be recoverable from storage.
			stored, err := st.Get(id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stored.Hash == secret {
				t.Fatal("secret stored in clear")
			}
		})
	}
}

func TestVerifyRejects(t *testing.T) {
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			token, err := st.Create(&Key{UserID: "u1"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			id, _, _ := ParseToken(token)

			cases := map[string]string{
				"empty":         "",
				"no prefix":     "nope_" + id + "_zzz",
				"wrong secret":  formatToken(id, "not-the-secret"),
				"unknown id":    formatToken("aaaaaaaaaaaaaaaaaaaaaaaa", "whatever"),
				"missing parts": "arcl_" + id,
				// A forged id must never reach the filesystem as a path.
				"traversal": "arcl_../../etc/passwd_x",
				"dotted":    "arcl_..._x",
			}
			for label, tok := range cases {
				if _, err := st.Verify(tok); err != ErrInvalid {
					t.Errorf("%s: Verify(%q) = %v, want ErrInvalid", label, tok, err)
				}
			}
		})
	}
}

func TestVerifyHonoursDisabledAndExpiry(t *testing.T) {
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			past := time.Now().Add(-time.Hour)
			expired, err := st.Create(&Key{UserID: "u1", ExpiresAt: &past})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := st.Verify(expired); err != ErrInvalid {
				t.Errorf("expired key verified: %v", err)
			}

			disabled, err := st.Create(&Key{UserID: "u1", Disabled: true})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := st.Verify(disabled); err != ErrInvalid {
				t.Errorf("disabled key verified: %v", err)
			}

			future := time.Now().Add(time.Hour)
			live, err := st.Create(&Key{UserID: "u1", ExpiresAt: &future})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := st.Verify(live); err != nil {
				t.Errorf("live key rejected: %v", err)
			}
		})
	}
}

func TestListDeleteTouch(t *testing.T) {
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			a, _ := st.Create(&Key{UserID: "u1", Label: "a"})
			_, _ = st.Create(&Key{UserID: "u1", Label: "b"})
			_, _ = st.Create(&Key{UserID: "u2", Label: "c"})

			mine, err := st.ListByUser("u1")
			if err != nil {
				t.Fatalf("ListByUser: %v", err)
			}
			if len(mine) != 2 {
				t.Fatalf("ListByUser = %d keys, want 2", len(mine))
			}
			all, err := st.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(all) != 3 {
				t.Fatalf("List = %d keys, want 3", len(all))
			}

			aid, _, _ := ParseToken(a)
			now := time.Now()
			if err := st.Touch(aid, now); err != nil {
				t.Fatalf("Touch: %v", err)
			}
			got, err := st.Get(aid)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.LastUsedAt == nil {
				t.Fatal("Touch did not record LastUsedAt")
			}

			if err := st.Delete(aid); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := st.Verify(a); err != ErrInvalid {
				t.Errorf("deleted key still verifies: %v", err)
			}
			if err := st.DeleteByUser("u1"); err != nil {
				t.Fatalf("DeleteByUser: %v", err)
			}
			left, _ := st.List()
			if len(left) != 1 {
				t.Fatalf("after DeleteByUser: %d keys, want 1", len(left))
			}
		})
	}
}

func TestScopeAndProjectChecks(t *testing.T) {
	k := &Key{Scopes: []string{"resources"}, Projects: []string{"p1"}}
	if !k.HasScope("resources") || k.HasScope("deploy") {
		t.Error("HasScope wrong")
	}
	if !k.AllowsProject("p1") || k.AllowsProject("p2") {
		t.Error("AllowsProject wrong for a restricted key")
	}
	open := &Key{}
	if !open.AllowsProject("anything") {
		t.Error("unrestricted key should allow every project")
	}
	if open.HasScope("resources") {
		t.Error("a key with no scopes must grant nothing (read-only)")
	}
}
