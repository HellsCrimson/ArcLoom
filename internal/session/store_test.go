package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newStores returns the two Store implementations to run the shared table
// against. Disk is rooted in a fresh temp dir.
func newStores(t *testing.T) map[string]Store {
	t.Helper()
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]Store{"memory": NewMemory(), "disk": d}
}

func TestStore_CreateGet(t *testing.T) {
	for name, s := range newStores(t) {
		t.Run(name, func(t *testing.T) {
			sess, err := s.Create("user-1", "local", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if sess.Token == "" {
				t.Fatal("expected a token")
			}
			got, err := s.Get(sess.Token)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.UserID != "user-1" || got.Provider != "local" {
				t.Fatalf("unexpected session: %+v", got)
			}
		})
	}
}

func TestStore_Expiry(t *testing.T) {
	for name, s := range newStores(t) {
		t.Run(name, func(t *testing.T) {
			sess, err := s.Create("user-1", "local", -time.Minute) // already expired
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(sess.Token); err != ErrNotFound {
				t.Fatalf("expected ErrNotFound for expired session, got %v", err)
			}
		})
	}
}

func TestStore_Delete(t *testing.T) {
	for name, s := range newStores(t) {
		t.Run(name, func(t *testing.T) {
			sess, _ := s.Create("user-1", "local", time.Hour)
			if err := s.Delete(sess.Token); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(sess.Token); err != ErrNotFound {
				t.Fatalf("expected ErrNotFound after delete, got %v", err)
			}
			// Deleting a missing token is a no-op.
			if err := s.Delete("nope"); err != nil {
				t.Fatalf("delete missing token should be nil, got %v", err)
			}
		})
	}
}

func TestStore_DeleteByUser(t *testing.T) {
	for name, s := range newStores(t) {
		t.Run(name, func(t *testing.T) {
			a1, _ := s.Create("alice", "local", time.Hour)
			a2, _ := s.Create("alice", "local", time.Hour)
			b1, _ := s.Create("bob", "local", time.Hour)
			if err := s.DeleteByUser("alice"); err != nil {
				t.Fatal(err)
			}
			for _, tok := range []string{a1.Token, a2.Token} {
				if _, err := s.Get(tok); err != ErrNotFound {
					t.Fatalf("alice session %s should be revoked", tok)
				}
			}
			if _, err := s.Get(b1.Token); err != nil {
				t.Fatalf("bob session should survive, got %v", err)
			}
		})
	}
}

func TestStore_GC(t *testing.T) {
	for name, s := range newStores(t) {
		t.Run(name, func(t *testing.T) {
			live, _ := s.Create("u", "local", time.Hour)
			_, _ = s.Create("u", "local", -time.Minute) // expired
			if err := s.GC(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(live.Token); err != nil {
				t.Fatalf("live session should survive GC, got %v", err)
			}
		})
	}
}

func TestDisk_SurvivesReload(t *testing.T) {
	dir := t.TempDir()
	d1, _ := NewDisk(dir)
	sess, _ := d1.Create("user-1", "local", time.Hour)

	// A fresh store instance (simulating a process restart) still sees it.
	d2, _ := NewDisk(dir)
	if _, err := d2.Get(sess.Token); err != nil {
		t.Fatalf("session should survive restart, got %v", err)
	}
}

// A forged cookie value that tries to traverse out of the sessions directory
// is rejected before it is joined into a path: Get returns ErrNotFound (never
// reading the targeted file) and Delete is a no-op.
func TestDisk_RejectsTraversalToken(t *testing.T) {
	root := t.TempDir()
	// A valid-looking session file sitting one level above the sessions root.
	outside := filepath.Join(filepath.Dir(root), "outside.yaml")
	if err := os.WriteFile(outside, []byte("token: x\nuserID: attacker\nexpiresAt: 2999-01-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := NewDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	// path("../outside") would resolve to the file above; the guard must stop it.
	if _, err := d.Get("../outside"); err != ErrNotFound {
		t.Fatalf("traversal token Get: got %v, want ErrNotFound", err)
	}
	if err := d.Delete("../../etc/anything"); err != nil {
		t.Fatalf("traversal token Delete: got %v, want nil no-op", err)
	}
	// Sanity: the outside file is untouched.
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file should be untouched: %v", err)
	}
}

func TestValidToken(t *testing.T) {
	ok := []string{"abcXYZ0-9_", "Zm9vYmFy"}
	bad := []string{"", "../foo", "a/b", "a.b", "a\\b", "a b"}
	for _, s := range ok {
		if !validToken(s) {
			t.Errorf("validToken(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validToken(s) {
			t.Errorf("validToken(%q) = true, want false", s)
		}
	}
}
