package secrets

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// memBackend is an in-memory Backend for exercising the Store's routing.
type memBackend struct {
	docs    map[string]map[string]string
	loadErr error
	loads   int
}

func newMemBackend() *memBackend { return &memBackend{docs: map[string]map[string]string{}} }

func (m *memBackend) Name() string { return "vault" }
func (m *memBackend) Load(_ context.Context, id string) (map[string]string, error) {
	m.loads++
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	out := map[string]string{}
	for k, v := range m.docs[id] {
		out[k] = v
	}
	return out, nil
}
func (m *memBackend) Store(_ context.Context, id string, values map[string]string) error {
	cp := map[string]string{}
	for k, v := range values {
		cp[k] = v
	}
	m.docs[id] = cp
	return nil
}
func (m *memBackend) Check(context.Context) error { return nil }

func TestStoreRoutesToRemote(t *testing.T) {
	s := NewStore(t.TempDir(), "master-key")
	if s.BackendName() != "local" || !s.Writable() {
		t.Fatalf("expected a local writable store, got %q", s.BackendName())
	}
	// A secret written before the switch lands locally.
	if err := s.Set("p1", "local_only", "v-local"); err != nil {
		t.Fatal(err)
	}

	mem := newMemBackend()
	s.SetRemote(mem)
	if s.BackendName() != "vault" {
		t.Fatalf("backend name = %q", s.BackendName())
	}

	if err := s.Set("p1", "remote_secret", "v-remote"); err != nil {
		t.Fatal(err)
	}
	if mem.docs["p1"]["remote_secret"] != "v-remote" {
		t.Fatalf("write did not reach the remote: %v", mem.docs)
	}

	// The local file is no longer consulted: a value only in local must not
	// resolve while a remote store is configured, or ArcLoom would serve secrets
	// from a store the operator moved away from.
	if _, found, err := s.Resolve("p1", "local_only"); err != nil || found {
		t.Fatalf("local secret leaked through the remote backend (found=%v, err=%v)", found, err)
	}
	v, found, err := s.Resolve("p1", "remote_secret")
	if err != nil || !found || v != "v-remote" {
		t.Fatalf("resolve = %q %v %v", v, found, err)
	}

	// Listing reports the remote's name as the backend.
	metas, err := s.List("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Name != "remote_secret" || metas[0].Backend != "vault" {
		t.Fatalf("list = %+v", metas)
	}

	// Deleting goes to the remote too, and clearing it restores the local view.
	if err := s.Delete("p1", "remote_secret"); err != nil {
		t.Fatal(err)
	}
	if len(mem.docs["p1"]) != 0 {
		t.Fatalf("delete did not reach the remote: %v", mem.docs)
	}
	s.SetRemote(nil)
	if v, found, _ := s.Resolve("p1", "local_only"); !found || v != "v-local" {
		t.Fatalf("local secret should be readable again after clearing the remote")
	}
}

func TestStoreEnvFallbackWithRemote(t *testing.T) {
	t.Setenv("ARCLOOM_SECRET_env_value", "from-env")
	s := NewStore(t.TempDir(), "master-key")
	s.SetRemote(newMemBackend())

	// Env stays a read-only fallback whichever writable backend is in use.
	v, found, err := s.Resolve("p1", "env_value")
	if err != nil || !found || v != "from-env" {
		t.Fatalf("env fallback = %q %v %v", v, found, err)
	}
	metas, _ := s.List("p1")
	if len(metas) != 1 || metas[0].Backend != "env" {
		t.Fatalf("list = %+v", metas)
	}
}

func TestStoreRemoteErrorSurfaces(t *testing.T) {
	s := NewStore(t.TempDir(), "master-key")
	mem := newMemBackend()
	mem.loadErr = errors.New("vault is sealed")
	s.SetRemote(mem)

	// A broken store must fail loudly: silently falling back to local would run a
	// plan with the wrong (or no) credentials.
	if _, err := s.List("p1"); err == nil {
		t.Fatal("expected the remote failure to surface")
	}
	if _, _, err := s.Resolve("p1", "x"); err == nil {
		t.Fatal("expected the remote failure to surface on resolve")
	}
	if err := s.Set("p1", "x", "y"); err == nil {
		t.Fatal("expected the remote failure to surface on set")
	}
}

func TestStoreWritableWithoutLocalKey(t *testing.T) {
	s := NewStore(t.TempDir(), "") // no ARCLOOM_SECRET_KEY
	if s.Writable() || s.BackendName() != "" {
		t.Fatalf("a keyless store must not be writable, got %q", s.BackendName())
	}
	if err := s.Set("p1", "a", "b"); !errors.Is(err, ErrNoWritableBackend) {
		t.Fatalf("want ErrNoWritableBackend, got %v", err)
	}
	// Configuring a remote makes it writable even with no master key — that is the
	// point of an external store.
	s.SetRemote(newMemBackend())
	if !s.Writable() || s.BackendName() != "vault" {
		t.Fatalf("remote should make the store writable, got %q", s.BackendName())
	}
	if err := s.Set("p1", "a", "b"); err != nil {
		t.Fatalf("set with remote only: %v", err)
	}
}

func TestMigrateToCopiesAndMerges(t *testing.T) {
	s := NewStore(t.TempDir(), "master-key")
	if err := s.Set("p1", "one", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("p2", "two", "2"); err != nil {
		t.Fatal(err)
	}

	dst := newMemBackend()
	// A value already in the destination must survive the migration.
	_ = dst.Store(context.Background(), "p1", map[string]string{"kept": "yes"})

	n, err := s.MigrateTo(context.Background(), dst, []string{"p1", "p2", "p3-empty"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 2 {
		t.Fatalf("copied = %d, want 2", n)
	}
	if dst.docs["p1"]["one"] != "1" || dst.docs["p1"]["kept"] != "yes" {
		t.Fatalf("p1 = %v", dst.docs["p1"])
	}
	if dst.docs["p2"]["two"] != "2" {
		t.Fatalf("p2 = %v", dst.docs["p2"])
	}
	if _, ok := dst.docs["p3-empty"]; ok {
		t.Errorf("a project with no secrets should not create a document")
	}
}

func TestMigrateToReportsPartialProgress(t *testing.T) {
	s := NewStore(t.TempDir(), "master-key")
	_ = s.Set("p1", "one", "1")

	if _, err := s.MigrateTo(context.Background(), nil, []string{"p1"}); err == nil {
		t.Fatal("a nil destination must be rejected")
	}
}

func TestMetasFromSorting(t *testing.T) {
	metas := metasFrom(map[string]string{"b": "1", "a": "2"}, map[string]string{"c": "3", "a": "shadowed"}, "vault")
	names := make([]string, 0, len(metas))
	for _, m := range metas {
		names = append(names, m.Name+":"+m.Backend)
	}
	// Sorted by name, and a name present in both is attributed to the writable
	// backend (which is what Resolve would return).
	if !slices.Equal(names, []string{"a:vault", "b:vault", "c:env"}) {
		t.Fatalf("metas = %v", names)
	}
}
