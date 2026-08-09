package oauth

import (
	"errors"
	"testing"
	"time"
)

// TestRefreshReuseRevokesGrant: rotation alone makes a stolen refresh token stop
// working, but it does not reveal that a theft happened, and whichever party
// redeems second is locked out — as likely the legitimate client as the thief.
// Seeing a previous generation means two parties hold the grant, and the only
// safe answer is to revoke it for both.
func TestRefreshReuseRevokesGrant(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := NewToken()
	g := &Grant{ClientID: "c1", UserID: "u1", RefreshHash: HashToken(first),
		ExpiresAt: time.Now().Add(time.Hour)}
	if err := d.SaveGrant(g); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveAccess(&AccessToken{Hash: HashToken("access-tok"), GrantID: g.ID,
		ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	// Normal rotation: the client exchanges its token for a new one.
	got, err := d.GrantByRefresh(first)
	if err != nil {
		t.Fatalf("first use must succeed: %v", err)
	}
	second, _ := NewToken()
	got.PrevRefreshHash = got.RefreshHash
	got.RefreshHash = HashToken(second)
	if err := d.SaveGrant(got); err != nil {
		t.Fatal(err)
	}

	// The new token still works.
	if _, err := d.GrantByRefresh(second); err != nil {
		t.Fatalf("rotated token must work: %v", err)
	}

	// The OLD one is replayed — the thief, or the client that never saw the
	// rotation land.
	if _, err := d.GrantByRefresh(first); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("replay must be reported as reuse, got %v", err)
	}
	// ...and the whole grant is gone, for both parties.
	if _, err := d.GetGrant(g.ID); err == nil {
		t.Error("the grant must be revoked after a replay")
	}
	if _, err := d.GrantByRefresh(second); err == nil {
		t.Error("the current token must stop working too — the grant is revoked")
	}
	if _, err := d.GrantByAccess("access-tok"); err == nil {
		t.Error("access tokens derived from the grant must be dropped")
	}
}
