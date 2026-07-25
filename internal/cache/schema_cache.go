// Package cache stores normalized provider schemas keyed by engine, provider
// source, and resolved version, avoiding repeated init+schema round-trips.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/HellsCrimson/ArcLoom/internal/schema"
)

// SchemaCache is a two-tier (memory + disk) cache of normalized schemas.
type SchemaCache struct {
	dir string

	mu  sync.RWMutex
	mem map[string]schema.NormalizedSchema
}

// New creates a cache rooted at dir (created lazily on first write).
func New(dir string) *SchemaCache {
	return &SchemaCache{dir: dir, mem: map[string]schema.NormalizedSchema{}}
}

// Key builds a stable cache key. version should be the version actually
// resolved by init (not a "latest" constraint) so stale schemas are not served.
// schema.FormatVersion is part of the key so an ArcLoom upgrade that changes
// normalization does not keep serving schemas normalized by the old code —
// entries written under a previous format are simply never looked up again.
func Key(engine, source, version, resourceType string) string {
	return strings.NewReplacer("/", "_", ":", "_").Replace(
		"v" + strconv.Itoa(schema.FormatVersion) + "|" +
			engine + "|" + source + "|" + version + "|" + resourceType,
	)
}

func (c *SchemaCache) Get(key string) (schema.NormalizedSchema, bool) {
	c.mu.RLock()
	if ns, ok := c.mem[key]; ok {
		c.mu.RUnlock()
		return ns, true
	}
	c.mu.RUnlock()

	// Fall back to disk.
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return schema.NormalizedSchema{}, false
	}
	var ns schema.NormalizedSchema
	if json.Unmarshal(data, &ns) != nil {
		return schema.NormalizedSchema{}, false
	}
	c.mu.Lock()
	c.mem[key] = ns
	c.mu.Unlock()
	return ns, true
}

func (c *SchemaCache) Put(key string, ns schema.NormalizedSchema) {
	c.mu.Lock()
	c.mem[key] = ns
	c.mu.Unlock()

	if c.dir == "" {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	if data, err := json.Marshal(ns); err == nil {
		_ = os.WriteFile(c.path(key), data, 0o644)
	}
}

func (c *SchemaCache) path(key string) string {
	return filepath.Join(c.dir, key+".json")
}
