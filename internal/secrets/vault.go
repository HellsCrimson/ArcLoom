package secrets

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Vault stores secrets in HashiCorp Vault or OpenBao via the KV v2 HTTP API.
// It is hand-rolled rather than built on the official client: ArcLoom needs four
// calls (read, write, token check, mount check), and vendoring the full Vault SDK
// for that would dwarf the rest of the binary — the same reasoning as
// internal/registry and internal/notify.
//
// Layout mirrors the local backend: ONE document per project, at
// <mount>/data/<prefix>/<projectID>, holding name → value. Read-modify-write on
// that document gives identical semantics to rewriting the project's .enc file.
type Vault struct {
	cfg    VaultConfig
	client *http.Client

	// versions remembers the KV version each project document was last read at,
	// so the following write can send it as a check-and-set. Store.mutate always
	// reads before writing inside one lock, so the value is fresh; without CAS a
	// concurrent writer (a second ArcLoom, or a human in the Vault UI) would be
	// silently overwritten by our read-modify-write.
	mu       sync.Mutex
	versions map[string]int
}

// VaultConfig is the connection configuration. Token may be empty at construction
// only for a Check against an unauthenticated endpoint — every data call needs it.
type VaultConfig struct {
	Address string
	Token   string
	// Mount is the KV v2 mount point ("secret" when empty).
	Mount string
	// Prefix is the path inside the mount that holds ArcLoom's documents
	// ("arcloom" when empty), so ArcLoom never has to own the whole mount.
	Prefix string
	// Namespace sets X-Vault-Namespace (Vault Enterprise / OpenBao namespaces).
	Namespace string
	// InsecureSkipVerify accepts a self-signed TLS certificate — common for a
	// homelab Vault, off by default.
	InsecureSkipVerify bool
}

const (
	defaultVaultMount  = "secret"
	defaultVaultPrefix = "arcloom"
)

// NewVault validates the configuration and builds the client.
func NewVault(cfg VaultConfig) (*Vault, error) {
	addr := strings.TrimRight(strings.TrimSpace(cfg.Address), "/")
	if addr == "" {
		return nil, errors.New("vault address is required")
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid vault address %q (expected http(s)://host:port)", cfg.Address)
	}
	cfg.Address = addr
	if cfg.Mount = strings.Trim(strings.TrimSpace(cfg.Mount), "/"); cfg.Mount == "" {
		cfg.Mount = defaultVaultMount
	}
	if cfg.Prefix = strings.Trim(strings.TrimSpace(cfg.Prefix), "/"); cfg.Prefix == "" {
		cfg.Prefix = defaultVaultPrefix
	}
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.Namespace = strings.TrimSpace(cfg.Namespace)

	client := &http.Client{Timeout: backendTimeout}
	if cfg.InsecureSkipVerify {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	return &Vault{cfg: cfg, client: client, versions: map[string]int{}}, nil
}

func (v *Vault) Name() string { return "vault" }

// Description is a credential-free summary for the settings UI and logs.
func (v *Vault) Description() string {
	return fmt.Sprintf("%s (%s/%s)", v.cfg.Address, v.cfg.Mount, v.cfg.Prefix)
}

// dataPath is the KV v2 data endpoint for a project's document.
func (v *Vault) dataPath(projectID string) (string, error) {
	// The id becomes a URL path segment, so it gets the same guard as the local
	// backend's file name.
	if !validProjectID(projectID) {
		return "", fmt.Errorf("invalid project id %q", projectID)
	}
	return fmt.Sprintf("%s/v1/%s/data/%s/%s", v.cfg.Address, v.cfg.Mount, v.cfg.Prefix, projectID), nil
}

// Load reads a project's document. A missing document is an empty map, not an
// error — a project simply has no secrets yet.
func (v *Vault) Load(ctx context.Context, projectID string) (map[string]string, error) {
	endpoint, err := v.dataPath(projectID)
	if err != nil {
		return nil, err
	}
	status, body, err := v.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		if isMissingMount(body) {
			return nil, fmt.Errorf("KV mount %q not found (is it a KV v2 mount?)", v.cfg.Mount)
		}
		return map[string]string{}, nil
	}
	if err := vaultStatusError(status, body); err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Data     map[string]any `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode vault response: %w", err)
	}
	v.mu.Lock()
	v.versions[projectID] = resp.Data.Metadata.Version
	v.mu.Unlock()

	out := make(map[string]string, len(resp.Data.Data))
	for k, raw := range resp.Data.Data {
		// Tolerate a document a human wrote with non-string values (port: 8080):
		// everything becomes a TF_VAR string in the end anyway.
		switch t := raw.(type) {
		case string:
			out[k] = t
		case nil:
			// A deleted key in Vault's own UI shows up as null; skip it.
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out, nil
}

// Store replaces a project's document, using the version from the preceding Load
// as a check-and-set so a concurrent change is reported instead of clobbered.
func (v *Vault) Store(ctx context.Context, projectID string, values map[string]string) error {
	endpoint, err := v.dataPath(projectID)
	if err != nil {
		return err
	}
	payload := map[string]any{"data": values}
	v.mu.Lock()
	version, known := v.versions[projectID]
	v.mu.Unlock()
	if known {
		payload["options"] = map[string]any{"cas": version}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	status, respBody, err := v.do(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	if status == http.StatusBadRequest && bytes.Contains(respBody, []byte("check-and-set")) {
		return errors.New("the secret was modified in Vault since it was read; retry")
	}
	if status == http.StatusNotFound && isMissingMount(respBody) {
		return fmt.Errorf("KV mount %q not found (is it a KV v2 mount?)", v.cfg.Mount)
	}
	if err := vaultStatusError(status, respBody); err != nil {
		return err
	}
	// The write produced a new version; remember it so a second write in the same
	// process does not fail its check-and-set.
	var resp struct {
		Data struct {
			Version int `json:"version"`
		} `json:"data"`
	}
	if json.Unmarshal(respBody, &resp) == nil && resp.Data.Version > 0 {
		v.mu.Lock()
		v.versions[projectID] = resp.Data.Version
		v.mu.Unlock()
	}
	return nil
}

// Check verifies, in order, that the address is reachable, the token is valid,
// and the mount exists — so the settings page can say which one is wrong.
func (v *Vault) Check(ctx context.Context) error {
	if v.cfg.Token == "" {
		return errors.New("vault token is required")
	}
	status, body, err := v.do(ctx, http.MethodGet, v.cfg.Address+"/v1/auth/token/lookup-self", nil)
	if err != nil {
		return err
	}
	if err := vaultStatusError(status, body); err != nil {
		return err
	}
	// A read of the prefix is enough to tell a wrong mount (404 "no handler")
	// from an empty one (plain 404) and from a policy that forbids it (403).
	probe := fmt.Sprintf("%s/v1/%s/metadata/%s", v.cfg.Address, v.cfg.Mount, v.cfg.Prefix)
	status, body, err = v.do(ctx, http.MethodGet, probe, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		if isMissingMount(body) {
			return fmt.Errorf("KV mount %q not found (is it a KV v2 mount?)", v.cfg.Mount)
		}
		return nil // reachable, authorized, nothing stored yet
	}
	return vaultStatusError(status, body)
}

func (v *Vault) do(ctx context.Context, method, endpoint string, body []byte) (int, []byte, error) {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return 0, nil, err
	}
	if v.cfg.Token != "" {
		req.Header.Set("X-Vault-Token", v.cfg.Token)
	}
	if v.cfg.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.cfg.Namespace)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.client.Do(req)
	if err != nil {
		// Never surface the token; the URL is safe (it holds no credential). The
		// caller prefixes the backend name, so this must not repeat it.
		return 0, nil, fmt.Errorf("%s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	// Vault errors are small JSON documents; cap the read so a misconfigured
	// address pointing at something else cannot stream into memory.
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(io.LimitReader(resp.Body, 1<<20)); err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, buf.Bytes(), nil
}

// vaultStatusError maps a non-2xx status onto an actionable message.
func vaultStatusError(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	detail := vaultErrors(body)
	switch status {
	case http.StatusForbidden:
		return fmt.Errorf("vault denied the request — check the token and its policy%s", detail)
	case http.StatusNotFound:
		return fmt.Errorf("vault path not found%s", detail)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("vault is sealed or unavailable%s", detail)
	default:
		return fmt.Errorf("vault returned %d%s", status, detail)
	}
}

// vaultErrors extracts Vault's own error strings for the message tail.
func vaultErrors(body []byte) string {
	var e struct {
		Errors []string `json:"errors"`
	}
	if json.Unmarshal(body, &e) != nil || len(e.Errors) == 0 {
		return ""
	}
	return ": " + strings.Join(e.Errors, "; ")
}

// isMissingMount distinguishes "this mount does not exist" from "this secret does
// not exist": both are 404, but only the former carries Vault's routing error.
func isMissingMount(body []byte) bool {
	return bytes.Contains(body, []byte("no handler for route"))
}
