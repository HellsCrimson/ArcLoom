// Package registry queries the Terraform/OpenTofu provider registries for the
// versions available for a provider source, and reads the versions actually
// installed from a workspace lock file. Everything here is best-effort: a
// network failure returns an error the caller degrades on, so offline installs
// keep working.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	goversion "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// hostForEngine maps an engine name to its default registry host. Both hosts
// implement the same v1 provider registry protocol.
func hostForEngine(engine string) string {
	if engine == "terraform" {
		return "registry.terraform.io"
	}
	return "registry.opentofu.org"
}

// SplitSource breaks a provider source into (namespace, name). It accepts a
// bare name ("random" → hashicorp/random), a namespaced source
// ("hashicorp/random") or a fully-qualified one
// ("registry.terraform.io/hashicorp/random").
func SplitSource(source string) (namespace, name string) {
	parts := strings.Split(strings.Trim(source, "/"), "/")
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return "hashicorp", parts[0]
	default:
		return parts[len(parts)-2], parts[len(parts)-1]
	}
}

type versionsResponse struct {
	Versions []struct {
		Version string `json:"version"`
	} `json:"versions"`
}

type cachedResult struct {
	versions  []*goversion.Version
	err       error
	fetchedAt time.Time
}

// Client fetches available provider versions, caching results in memory so
// repeated checks (and offline retries) don't hammer the registry.
type Client struct {
	http   *http.Client
	ttl    time.Duration // how long a successful result is reused
	negTTL time.Duration // how long a failure is remembered before retrying

	mu    sync.Mutex
	cache map[string]cachedResult // key: engine + "|" + namespace/name
	now   func() time.Time
}

// NewClient builds a registry client with sensible best-effort defaults.
func NewClient() *Client {
	return &Client{
		http:   &http.Client{Timeout: 8 * time.Second},
		ttl:    6 * time.Hour,
		negTTL: 5 * time.Minute,
		cache:  map[string]cachedResult{},
		now:    time.Now,
	}
}

// AvailableVersions returns every published version of source (newest first),
// fetched from the engine's registry and cached. On a network/parse failure it
// returns the error; callers treat that as "unknown" and carry on.
func (c *Client) AvailableVersions(ctx context.Context, engine, source string) ([]*goversion.Version, error) {
	ns, name := SplitSource(source)
	if ns == "" || name == "" {
		return nil, fmt.Errorf("invalid provider source %q", source)
	}
	key := engine + "|" + ns + "/" + name

	c.mu.Lock()
	if cached, ok := c.cache[key]; ok {
		age := c.now().Sub(cached.fetchedAt)
		if (cached.err == nil && age < c.ttl) || (cached.err != nil && age < c.negTTL) {
			c.mu.Unlock()
			return cached.versions, cached.err
		}
	}
	c.mu.Unlock()

	versions, err := c.fetch(ctx, engine, ns, name)

	c.mu.Lock()
	c.cache[key] = cachedResult{versions: versions, err: err, fetchedAt: c.now()}
	c.mu.Unlock()
	return versions, err
}

func (c *Client) fetch(ctx context.Context, engine, ns, name string) ([]*goversion.Version, error) {
	url := fmt.Sprintf("https://%s/v1/providers/%s/%s/versions", hostForEngine(engine), ns, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %s", resp.Status)
	}
	var body versionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]*goversion.Version, 0, len(body.Versions))
	for _, v := range body.Versions {
		if parsed, err := goversion.NewVersion(v.Version); err == nil {
			out = append(out, parsed)
		}
	}
	sort.Sort(sort.Reverse(goversion.Collection(out)))
	return out, nil
}

// LatestStable returns the highest non-prerelease version in vs, or "" when
// there is none. vs may be in any order.
func LatestStable(vs []*goversion.Version) string {
	var best *goversion.Version
	for _, v := range vs {
		if v.Prerelease() != "" {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
		}
	}
	if best == nil {
		return ""
	}
	return best.Original()
}

// InstalledVersions parses a .terraform.lock.hcl file and returns the concrete
// version locked for each provider, keyed by fully-qualified source
// (e.g. "registry.terraform.io/hashicorp/random"). A missing or malformed lock
// file yields an empty map — the workspace was simply never initialised.
func InstalledVersions(lockPath string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return out
	}
	f, diags := hclparse.NewParser().ParseHCL(data, lockPath)
	if diags.HasErrors() || f == nil {
		return out
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return out
	}
	for _, blk := range body.Blocks {
		if blk.Type != "provider" || len(blk.Labels) != 1 {
			continue
		}
		attr, ok := blk.Body.Attributes["version"]
		if !ok {
			continue
		}
		v, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || v.Type() != cty.String {
			continue
		}
		out[blk.Labels[0]] = v.AsString()
	}
	return out
}

// InstalledFor returns the locked version for a provider source, matching the
// source's namespace/name against the fully-qualified lock keys. "" if absent.
func InstalledFor(installed map[string]string, source string) string {
	ns, name := SplitSource(source)
	suffix := "/" + ns + "/" + name
	for fqn, ver := range installed {
		if fqn == ns+"/"+name || strings.HasSuffix(fqn, suffix) {
			return ver
		}
	}
	return ""
}
