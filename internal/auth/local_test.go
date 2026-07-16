package auth

import (
	"context"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/user"
)

func mkUser(t *testing.T, store user.Store, username, password string) {
	t.Helper()
	hash, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(&user.User{
		ID:           username,
		Username:     username,
		PasswordHash: hash,
		Provider:     ProviderLocal,
		CreatedAt:    time.Unix(0, 0),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalProvider_Authenticate(t *testing.T) {
	users := user.NewMemory()
	mkUser(t, users, "alice", "hunter2")
	p := NewLocalProvider(users)

	id, err := p.Authenticate(context.Background(), "alice", "hunter2")
	if err != nil {
		t.Fatalf("good creds: %v", err)
	}
	if id.Provider != ProviderLocal || id.Subject != "alice" {
		t.Fatalf("unexpected identity: %+v", id)
	}

	if _, err := p.Authenticate(context.Background(), "alice", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: want ErrInvalidCredentials, got %v", err)
	}
	if _, err := p.Authenticate(context.Background(), "nobody", "x"); err != ErrInvalidCredentials {
		t.Fatalf("unknown user: want ErrInvalidCredentials, got %v", err)
	}
}

func TestLocalProvider_DisabledAndSSOOnly(t *testing.T) {
	users := user.NewMemory()
	mkUser(t, users, "alice", "hunter2")
	_ = users.Update("alice", func(u *user.User) error { u.Disabled = true; return nil })
	_ = users.Create(&user.User{ID: "bob", Username: "bob", Provider: ProviderOIDC, Subject: "s"})
	p := NewLocalProvider(users)

	if _, err := p.Authenticate(context.Background(), "alice", "hunter2"); err != ErrInvalidCredentials {
		t.Fatalf("disabled user: want ErrInvalidCredentials, got %v", err)
	}
	if _, err := p.Authenticate(context.Background(), "bob", ""); err != ErrInvalidCredentials {
		t.Fatalf("sso-only user: want ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticator_Registry(t *testing.T) {
	a := New(NewLocalProvider(user.NewMemory()))
	if _, ok := a.Local(); !ok {
		t.Fatal("expected a local provider")
	}
	if got := a.Enabled(); len(got) != 1 || got[0] != ProviderLocal {
		t.Fatalf("Enabled = %v", got)
	}
	if _, ok := a.Redirect(ProviderOIDC); ok {
		t.Fatal("no OIDC provider should be registered")
	}
}
