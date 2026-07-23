package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/license"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// licenseTestServer returns a server (auth disabled so requests are unauthed but
// pass) plus the handler.
func licenseTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DefaultEngine: "tofu", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	return New(cfg, nil, store.NewMemory(), nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil)
}

func TestLicenseStatusEndpoint(t *testing.T) {
	srv := licenseTestServer(t)
	h := srv.Handler()

	get := func() licenseDTO {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /license = %d", rec.Code)
		}
		var dto licenseDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		return dto
	}

	// Default: community, no features, community seat limit.
	dto := get()
	if dto.Tier != license.TierCommunity || len(dto.Features) != 0 {
		t.Fatalf("expected community with no features, got %+v", dto)
	}
	if dto.SeatLimit != license.CommunitySeatLimit {
		t.Fatalf("expected community seat limit, got %d", dto.SeatLimit)
	}

	// Install enterprise (SSO+drift only) directly and re-read.
	srv.license.Install(&license.License{Licensee: "Acme", Tier: license.TierEnterprise, Features: []string{license.FeatureSSO, license.FeatureDrift}})
	dto = get()
	if dto.Tier != license.TierEnterprise || dto.Licensee != "Acme" {
		t.Fatalf("expected enterprise Acme, got %+v", dto)
	}
	if !contains(dto.Features, license.FeatureSSO) || !contains(dto.Features, license.FeatureDrift) {
		t.Fatalf("expected sso+drift features, got %v", dto.Features)
	}
	if contains(dto.Features, license.FeatureMultiUser) {
		t.Fatalf("multi_user should not be granted, got %v", dto.Features)
	}
}

// TestDriftGateWithoutLicense confirms enabling scheduled drift is rejected on a
// community install and allowed once licensed.
func TestDriftGateWithoutLicense(t *testing.T) {
	srv := licenseTestServer(t)
	// Give the store a project so the drift handler passes its existence check.
	if err := srv.store.Create(&model.Project{ID: "p-drift", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	body := `{"enabled":true,"intervalMinutes":15}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p-drift/drift", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without license, got %d (%s)", rec.Code, rec.Body.String())
	}

	srv.license.Install(&license.License{Tier: license.TierEnterprise})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p-drift/drift", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with license, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func contains(s []string, v string) bool { return slices.Contains(s, v) }
