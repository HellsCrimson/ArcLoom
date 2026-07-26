package secrets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Backend is an external secret store (today: Vault / OpenBao). It replaces the
// local encrypted files as the *writable* store when configured; the env backend
// stays available as a read-only fallback either way, so an operator can always
// inject a value the store does not hold.
//
// Values live under one document per project, mirroring the local backend's one
// .enc file per project: read-modify-write keeps the two implementations
// behaviourally identical, so nothing above this interface has to care which is
// in use.
type Backend interface {
	// Name identifies the backend in metadata and the UI ("vault").
	Name() string
	// Load returns every secret for a project. A project with no document yet is
	// not an error — it returns an empty map.
	Load(ctx context.Context, projectID string) (map[string]string, error)
	// Store replaces a project's whole secret document.
	Store(ctx context.Context, projectID string, values map[string]string) error
	// Check probes reachability, credentials and the configured path, for the
	// settings page's Test button. It must not mutate anything.
	Check(ctx context.Context) error
}

// backendTimeout bounds a single remote call. Secret resolution sits in the
// plan/apply path, so a hung store must fail rather than hang the run.
const backendTimeout = 10 * time.Second

// ErrNoWritableBackend is returned when neither the local encrypted store nor a
// remote backend is available to write to.
var ErrNoWritableBackend = errors.New("no writable secret store: set ARCLOOM_SECRET_KEY or configure an external store")

// SetRemote installs (or clears, with nil) the external backend. Safe to call at
// runtime: the settings page swaps the store without a restart, the same way SSO
// swaps its provider.
func (s *Store) SetRemote(b Backend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remote = b
}

// Remote returns the configured external backend, or nil when secrets are local.
func (s *Store) Remote() Backend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remote
}

// BackendName names the writable backend for the UI and the instance panel:
// the remote's name, "local", or "" when nothing is writable (values may still
// be readable from the environment).
func (s *Store) BackendName() string {
	if r := s.Remote(); r != nil {
		return r.Name()
	}
	if s.LocalEnabled() {
		return "local"
	}
	return ""
}

// Writable reports whether secrets can be created through the API. It is the
// remote backend OR the local encrypted files — env secrets are read-only by
// nature (they come from the host environment).
func (s *Store) Writable() bool { return s.BackendName() != "" }

// load reads a project's secrets from whichever backend is writable. The remote
// takes precedence: once configured, the local files are no longer consulted, so
// a value can never be served from a store the operator has moved away from.
func (s *Store) load(projectID string) (map[string]string, map[string]string, error) {
	if r := s.Remote(); r != nil {
		ctx, cancel := context.WithTimeout(context.Background(), backendTimeout)
		defer cancel()
		m, err := r.Load(ctx, projectID)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", r.Name(), err)
		}
		return m, envSecrets(), nil
	}
	s.mu.Lock()
	m, err := s.loadLocal(projectID)
	s.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return m, envSecrets(), nil
}

// MigrateTo copies every secret ArcLoom currently holds for the given projects
// into dst, so switching stores does not silently strand the values a project's
// resources reference. Returns the number of secrets copied; a per-project
// failure aborts (a partial move is reported rather than hidden).
func (s *Store) MigrateTo(ctx context.Context, dst Backend, projectIDs []string) (int, error) {
	if dst == nil {
		return 0, errors.New("no destination backend")
	}
	copied := 0
	for _, id := range projectIDs {
		src, _, err := s.load(id)
		if err != nil {
			return copied, fmt.Errorf("read %s: %w", id, err)
		}
		if len(src) == 0 {
			continue
		}
		// Merge into whatever the destination already has: a re-run of the same
		// migration must not drop secrets created directly in the target store.
		existing, err := dst.Load(ctx, id)
		if err != nil {
			return copied, fmt.Errorf("read destination for %s: %w", id, err)
		}
		if existing == nil {
			existing = map[string]string{}
		}
		for k, v := range src {
			existing[k] = v
		}
		if err := dst.Store(ctx, id, existing); err != nil {
			return copied, fmt.Errorf("write %s: %w", id, err)
		}
		copied += len(src)
	}
	return copied, nil
}

// metasFrom builds the value-free listing from a primary map + the env fallback.
func metasFrom(primary map[string]string, env map[string]string, backend string) []Meta {
	out := make([]Meta, 0, len(primary)+len(env))
	seen := make(map[string]bool, len(primary))
	for name := range primary {
		out = append(out, Meta{Name: name, Backend: backend})
		seen[name] = true
	}
	for name := range env {
		if !seen[name] {
			out = append(out, Meta{Name: name, Backend: "env"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
