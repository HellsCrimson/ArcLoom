package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeVault is a minimal KV v2 server: one versioned document per path, plus the
// token-lookup and metadata endpoints Check probes. It lets the client be tested
// against the real wire format without a Vault binary.
type fakeVault struct {
	server *httptest.Server
	mu     sync.Mutex
	docs   map[string]map[string]any // path -> data
	vers   map[string]int            // path -> current version
	token  string
	mount  string
	// requests records (method, path) for assertions about the calls made.
	requests []string
	// namespaces records the X-Vault-Namespace header per request.
	namespaces []string
}

func newFakeVault(t *testing.T) *fakeVault {
	t.Helper()
	f := &fakeVault{docs: map[string]map[string]any{}, vers: map[string]int{}, token: "root-token", mount: "secret"}
	mux := http.NewServeMux()
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.namespaces = append(f.namespaces, r.Header.Get("X-Vault-Namespace"))
		f.mu.Unlock()

		if r.Header.Get("X-Vault-Token") != f.token {
			writeVaultErr(w, http.StatusForbidden, "permission denied")
			return
		}
		if r.URL.Path == "/v1/auth/token/lookup-self" {
			writeVaultJSON(w, map[string]any{"data": map[string]any{"id": "hmac"}})
			return
		}
		// Everything else must live under the configured mount.
		if !strings.HasPrefix(r.URL.Path, "/v1/"+f.mount+"/") {
			writeVaultErr(w, http.StatusNotFound, "no handler for route \""+strings.TrimPrefix(r.URL.Path, "/v1/")+"\"")
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/"+f.mount+"/metadata/"):
			writeVaultErr(w, http.StatusNotFound, "")
		case strings.HasPrefix(r.URL.Path, "/v1/"+f.mount+"/data/"):
			f.handleData(w, r)
		default:
			writeVaultErr(w, http.StatusNotFound, "")
		}
	})
	return f
}

func (f *fakeVault) handleData(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/v1/"+f.mount+"/data/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		doc, ok := f.docs[key]
		if !ok {
			writeVaultErr(w, http.StatusNotFound, "")
			return
		}
		writeVaultJSON(w, map[string]any{"data": map[string]any{
			"data": doc, "metadata": map[string]any{"version": f.vers[key]},
		}})
	case http.MethodPost:
		var body struct {
			Data    map[string]any `json:"data"`
			Options struct {
				CAS *int `json:"cas"`
			} `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeVaultErr(w, http.StatusBadRequest, "bad body")
			return
		}
		if body.Options.CAS != nil && *body.Options.CAS != f.vers[key] {
			writeVaultErr(w, http.StatusBadRequest, "check-and-set parameter did not match the current version")
			return
		}
		f.docs[key] = body.Data
		f.vers[key]++
		writeVaultJSON(w, map[string]any{"data": map[string]any{"version": f.vers[key]}})
	default:
		writeVaultErr(w, http.StatusMethodNotAllowed, "")
	}
}

func writeVaultJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeVaultErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	errs := []string{}
	if msg != "" {
		errs = append(errs, msg)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": errs})
}

func testVault(t *testing.T, f *fakeVault, mutate func(*VaultConfig)) *Vault {
	t.Helper()
	cfg := VaultConfig{Address: f.server.URL, Token: f.token}
	if mutate != nil {
		mutate(&cfg)
	}
	v, err := NewVault(cfg)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	return v
}

func TestVaultRoundTrip(t *testing.T) {
	f := newFakeVault(t)
	v := testVault(t, f, nil)
	ctx := context.Background()

	// A project with no document reads as empty, not as an error.
	got, err := v.Load(ctx, "proj1")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty load = %v, %v", got, err)
	}

	if err := v.Store(ctx, "proj1", map[string]string{"db_password": "s3cret"}); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err = v.Load(ctx, "proj1")
	if err != nil {
		t.Fatal(err)
	}
	if got["db_password"] != "s3cret" {
		t.Fatalf("round trip = %v", got)
	}

	// Default mount/prefix land where documented.
	f.mu.Lock()
	reqs := strings.Join(f.requests, " ")
	f.mu.Unlock()
	if !strings.Contains(reqs, "/v1/secret/data/arcloom/proj1") {
		t.Errorf("unexpected paths: %s", reqs)
	}
}

func TestVaultCustomMountPrefixAndNamespace(t *testing.T) {
	f := newFakeVault(t)
	f.mount = "kv"
	v := testVault(t, f, func(c *VaultConfig) {
		c.Mount = "/kv/"
		c.Prefix = "/team/arcloom/"
		c.Namespace = "eng"
	})
	if err := v.Store(context.Background(), "p", map[string]string{"a": "b"}); err != nil {
		t.Fatalf("store: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(strings.Join(f.requests, " "), "/v1/kv/data/team/arcloom/p") {
		t.Errorf("mount/prefix not trimmed into the path: %v", f.requests)
	}
	if f.namespaces[0] != "eng" {
		t.Errorf("namespace header = %q", f.namespaces[0])
	}
}

func TestVaultCheckDistinguishesFailures(t *testing.T) {
	f := newFakeVault(t)

	if err := testVault(t, f, nil).Check(context.Background()); err != nil {
		t.Fatalf("healthy check: %v", err)
	}

	// Bad token → the message points at the token, not at the address.
	err := testVault(t, f, func(c *VaultConfig) { c.Token = "wrong" }).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("bad token error = %v", err)
	}

	// Wrong mount → Vault's routing 404, reported as a mount problem rather than
	// as "no secrets yet".
	err = testVault(t, f, func(c *VaultConfig) { c.Mount = "nope" }).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mount") {
		t.Fatalf("wrong mount error = %v", err)
	}

	// No token configured at all is caught before any request.
	if err := testVault(t, f, func(c *VaultConfig) { c.Token = " " }).Check(context.Background()); err == nil {
		t.Fatal("empty token should fail the check")
	}
}

func TestVaultWrongMountOnLoad(t *testing.T) {
	f := newFakeVault(t)
	v := testVault(t, f, func(c *VaultConfig) { c.Mount = "nope" })
	if _, err := v.Load(context.Background(), "p"); err == nil || !strings.Contains(err.Error(), "mount") {
		t.Fatalf("want a mount error, got %v", err)
	}
}

func TestVaultCASConflictReported(t *testing.T) {
	f := newFakeVault(t)
	v := testVault(t, f, nil)
	ctx := context.Background()

	if err := v.Store(ctx, "p", map[string]string{"a": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Load(ctx, "p"); err != nil { // caches version 1
		t.Fatal(err)
	}
	// Someone else writes in between, bumping the version.
	f.mu.Lock()
	f.vers["arcloom/p"] = 7
	f.mu.Unlock()

	err := v.Store(ctx, "p", map[string]string{"a": "2"})
	if err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("a concurrent change must be reported, got %v", err)
	}
}

func TestVaultCoercesNonStringValues(t *testing.T) {
	f := newFakeVault(t)
	f.mu.Lock()
	f.docs["arcloom/p"] = map[string]any{"port": 8080, "on": true, "gone": nil, "name": "x"}
	f.vers["arcloom/p"] = 1
	f.mu.Unlock()

	got, err := testVault(t, f, nil).Load(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if got["port"] != "8080" || got["on"] != "true" || got["name"] != "x" {
		t.Fatalf("coercion = %v", got)
	}
	if _, ok := got["gone"]; ok {
		t.Errorf("a null value should be skipped, got %v", got)
	}
}

func TestVaultRejectsBadConfig(t *testing.T) {
	for _, addr := range []string{"", "  ", "not-a-url", "ftp://host", "https://"} {
		if _, err := NewVault(VaultConfig{Address: addr, Token: "t"}); err == nil {
			t.Errorf("address %q should be rejected", addr)
		}
	}
	// A project id that could escape the path is refused before any request.
	v, err := NewVault(VaultConfig{Address: "https://vault.example", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Load(context.Background(), "../etc"); err == nil {
		t.Error("a traversing project id must be rejected")
	}
}
