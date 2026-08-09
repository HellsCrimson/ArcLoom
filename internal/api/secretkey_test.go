package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// secretKeyServer builds an auth-disabled server (admin routes open) whose two
// encrypted stores share one master key, the arrangement a real install has.
func secretKeyServer(t *testing.T, masterKey string) (*Server, http.Handler, *secrets.Store, *settings.Store) {
	t.Helper()
	set, err := settings.NewStore(t.TempDir()+"/settings.yaml", masterKey)
	if err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewStore(t.TempDir(), masterKey)
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir(), SecretKey: masterKey}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	srv := New(cfg, nil, store.NewMemory(), nil, sec, nil, authn, session.NewMemory(), user.NewMemory(), set, nil, nil, nil, nil)
	return srv, srv.Handler(), sec, set
}

func rotateRequest(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/secret-key/rotate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestRotateSecretKey_ReKeysBothStores(t *testing.T) {
	srv, h, sec, set := secretKeyServer(t, "old-master-key")
	if err := sec.Set("proj1", "db_pw", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	if err := set.SetSSO(settings.SSO{Issuer: "https://idp", ClientID: "c", ClientSecret: "sso-secret"}); err != nil {
		t.Fatal(err)
	}

	rr := rotateRequest(t, h, `{"currentKey":"old-master-key","newKey":"a-brand-new-key"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp rotateKeyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Complete || resp.Secrets.Verified != 1 || resp.Settings.Verified != 1 {
		t.Fatalf("incomplete rotation: %+v", resp)
	}
	if len(resp.Warnings) == 0 || !strings.Contains(resp.Warnings[0], "ARCLOOM_SECRET_KEY") {
		t.Errorf("the environment-update warning is the whole point: %+v", resp.Warnings)
	}
	// The status echoed back must show a healthy store under the new key.
	if resp.Status.UnreadableFields != 0 || len(resp.Status.UnreadableSecrets) != 0 {
		t.Errorf("post-rotation status reports unreadable items: %+v", resp.Status)
	}
	// Values survive, and the live config now holds the new key.
	if v, ok, err := sec.Resolve("proj1", "db_pw"); err != nil || !ok || v != "s3cr3t" {
		t.Errorf("secret lost: %q %v %v", v, ok, err)
	}
	if set.SSO().ClientSecret != "sso-secret" {
		t.Error("settings secret lost")
	}
	if srv.cfg.SecretKey != "a-brand-new-key" {
		t.Error("server config still holds the old key")
	}
}

func TestRotateSecretKey_RejectsWrongOrWeakInput(t *testing.T) {
	_, h, sec, _ := secretKeyServer(t, "old-master-key")
	_ = sec.Set("proj1", "db_pw", "s3cr3t")

	cases := []struct {
		name, body string
		want       int
	}{
		{"wrong current key", `{"currentKey":"guessed","newKey":"a-brand-new-key"}`, http.StatusBadRequest},
		{"too short", `{"currentKey":"old-master-key","newKey":"short"}`, http.StatusBadRequest},
		{"unchanged", `{"currentKey":"old-master-key","newKey":"old-master-key"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rr := rotateRequest(t, h, tc.body); rr.Code != tc.want {
				t.Fatalf("status %d (want %d): %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
	// Nothing was touched by any of the refusals.
	if v, ok, err := sec.Resolve("proj1", "db_pw"); err != nil || !ok || v != "s3cr3t" {
		t.Errorf("a refused rotation damaged the store: %q %v %v", v, ok, err)
	}
}

// A rotation must not start while an engine run holds resolved secrets and the
// workspace lock.
func TestRotateSecretKey_RefusesWhileEngineBusy(t *testing.T) {
	srv, h, _, _ := secretKeyServer(t, "old-master-key")
	done := srv.beginEngineOp()
	rr := rotateRequest(t, h, `{"currentKey":"old-master-key","newKey":"a-brand-new-key"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status %d (want 409): %s", rr.Code, rr.Body.String())
	}
	done()
	if rr := rotateRequest(t, h, `{"currentKey":"old-master-key","newKey":"a-brand-new-key"}`); rr.Code != http.StatusOK {
		t.Fatalf("status %d after the run finished: %s", rr.Code, rr.Body.String())
	}
}

func TestRotateSecretKey_NoMasterKeyConfigured(t *testing.T) {
	_, h, _, _ := secretKeyServer(t, "")
	rr := rotateRequest(t, h, `{"currentKey":"","newKey":"a-brand-new-key"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d (want 400): %s", rr.Code, rr.Body.String())
	}
}

func TestGetSecretKey_ReportsUnreadableItems(t *testing.T) {
	// Seal under one key, then serve with another: the panel must be able to
	// say "N items cannot be read", not show an empty, healthy-looking store.
	dir := t.TempDir()
	setPath := t.TempDir() + "/settings.yaml"
	set, err := settings.NewStore(setPath, "right-key")
	if err != nil {
		t.Fatal(err)
	}
	_ = set.SetSSO(settings.SSO{Issuer: "i", ClientID: "c", ClientSecret: "sso-secret"})
	_ = secrets.NewStore(dir, "right-key").Set("proj1", "db_pw", "s3cr3t")

	wrongSet, err := settings.NewStore(setPath, "wrong-key")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir(), SecretKey: "wrong-key"}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	srv := New(cfg, nil, store.NewMemory(), nil, secrets.NewStore(dir, "wrong-key"), nil, authn,
		session.NewMemory(), user.NewMemory(), wrongSet, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings/secret-key", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var dto secretKeyDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.SecretFiles != 1 || len(dto.UnreadableSecrets) != 1 {
		t.Errorf("secret files: %+v", dto)
	}
	if dto.UnreadableFields != 1 || len(dto.SettingsFields) != 1 {
		t.Errorf("settings fields: %+v", dto.SettingsFields)
	}
	if !dto.KeySet {
		t.Error("keySet should be true when ARCLOOM_SECRET_KEY is configured")
	}
}

func TestRotateSecretKey_AuditActionMapped(t *testing.T) {
	if got := auditActions["POST /admin/settings/secret-key/rotate"]; got != "settings.secret_key.rotate" {
		t.Fatalf("audit action = %q", got)
	}
}
