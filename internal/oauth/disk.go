package oauth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Disk is a Store that keeps YAML files under root, one directory per kind:
//
//	<root>/clients/<id>.yaml
//	<root>/grants/<id>.yaml
//	<root>/codes/<hash>.yaml
//	<root>/tokens/<hash>.yaml
//
// Codes and access tokens are keyed by the token's *hash*, so a lookup is a
// single stat rather than a scan, and the plaintext token never appears in a
// filename. Grants are keyed by id because they are listed per user.
type Disk struct {
	root string
	mu   sync.Mutex
}

const (
	dirClients = "clients"
	dirGrants  = "grants"
	dirCodes   = "codes"
	dirTokens  = "tokens"
)

// NewDisk creates a disk-backed store rooted at dir.
func NewDisk(dir string) (*Disk, error) {
	for _, sub := range []string{dirClients, dirGrants, dirCodes, dirTokens} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("create oauth dir: %w", err)
		}
	}
	return &Disk{root: dir}, nil
}

func (d *Disk) path(kind, name string) string { return filepath.Join(d.root, kind, name+".yaml") }

func (d *Disk) write(kind, name string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(d.path(kind, name), data, 0o600)
}

func (d *Disk) read(kind, name string, v any) error {
	data, err := os.ReadFile(d.path(kind, name))
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return yaml.Unmarshal(data, v)
}

func (d *Disk) RegisterClient(c *Client) error {
	if c.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		c.ID = id
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.write(dirClients, c.ID, c)
}

func (d *Disk) GetClient(id string) (*Client, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var c Client
	if err := d.read(dirClients, id, &c); err != nil {
		return nil, err
	}
	c.ID = id
	return &c, nil
}

// CountClients counts registered clients. Registration is open and
// unauthenticated, so the HTTP layer uses this to cap the total.
func (d *Disk) CountClients() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(d.root, dirClients))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			n++
		}
	}
	return n, nil
}

func (d *Disk) SaveCode(c *Code) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.write(dirCodes, c.Hash, c)
}

// ConsumeCode deletes the code before returning it, so a replay finds nothing.
func (d *Disk) ConsumeCode(code string) (*Code, error) {
	h := HashToken(code)
	d.mu.Lock()
	defer d.mu.Unlock()
	var c Code
	if err := d.read(dirCodes, h, &c); err != nil {
		return nil, ErrInvalid
	}
	_ = os.Remove(d.path(dirCodes, h))
	if time.Now().After(c.ExpiresAt) {
		return nil, ErrInvalid
	}
	c.Hash = h
	return &c, nil
}

func (d *Disk) SaveGrant(g *Grant) error {
	if g.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		g.ID = id
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.write(dirGrants, g.ID, g)
}

func (d *Disk) GetGrant(id string) (*Grant, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loadGrant(id)
}

func (d *Disk) loadGrant(id string) (*Grant, error) {
	var g Grant
	if err := d.read(dirGrants, id, &g); err != nil {
		return nil, err
	}
	g.ID = id
	return &g, nil
}

func (d *Disk) GrantByRefresh(refresh string) (*Grant, error) {
	h := HashToken(refresh)
	d.mu.Lock()
	defer d.mu.Unlock()
	grants, err := d.allGrants()
	if err != nil {
		return nil, err
	}
	for _, g := range grants {
		if g.RefreshHash != "" && subtleEqual(g.RefreshHash, h) {
			if time.Now().After(g.ExpiresAt) {
				return nil, ErrInvalid
			}
			return g, nil
		}
	}
	return nil, ErrInvalid
}

func (d *Disk) ListGrants(userID string) ([]*Grant, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	all, err := d.allGrants()
	if err != nil {
		return nil, err
	}
	out := []*Grant{}
	for _, g := range all {
		if g.UserID == userID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (d *Disk) DeleteGrant(id string) error {
	if !validID(id) {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.Remove(d.path(dirGrants, id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Access tokens outlive their grant file otherwise, which would let a
	// revoked agent keep working until its token expired.
	return d.dropTokensFor(id)
}

func (d *Disk) DeleteGrantsByUser(userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	all, err := d.allGrants()
	if err != nil {
		return err
	}
	for _, g := range all {
		if g.UserID != userID {
			continue
		}
		_ = os.Remove(d.path(dirGrants, g.ID))
		_ = d.dropTokensFor(g.ID)
	}
	return nil
}

func (d *Disk) SaveAccess(t *AccessToken) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.write(dirTokens, t.Hash, t)
}

func (d *Disk) GrantByAccess(token string) (*Grant, error) {
	h := HashToken(token)
	d.mu.Lock()
	defer d.mu.Unlock()
	var t AccessToken
	if err := d.read(dirTokens, h, &t); err != nil {
		return nil, ErrInvalid
	}
	if time.Now().After(t.ExpiresAt) {
		_ = os.Remove(d.path(dirTokens, h))
		return nil, ErrInvalid
	}
	g, err := d.loadGrant(t.GrantID)
	if err != nil || time.Now().After(g.ExpiresAt) {
		return nil, ErrInvalid
	}
	return g, nil
}

func (d *Disk) GC() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for _, kind := range []string{dirCodes, dirTokens} {
		entries, err := os.ReadDir(filepath.Join(d.root, kind))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".yaml")
			var exp time.Time
			if kind == dirCodes {
				var c Code
				if d.read(kind, name, &c) == nil {
					exp = c.ExpiresAt
				}
			} else {
				var t AccessToken
				if d.read(kind, name, &t) == nil {
					exp = t.ExpiresAt
				}
			}
			if !exp.IsZero() && now.After(exp) {
				_ = os.Remove(d.path(kind, name))
			}
		}
	}
	return nil
}

// dropTokensFor removes every access token issued from a grant. Caller holds
// the lock.
func (d *Disk) dropTokensFor(grantID string) error {
	entries, err := os.ReadDir(filepath.Join(d.root, dirTokens))
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".yaml")
		var t AccessToken
		if d.read(dirTokens, name, &t) == nil && t.GrantID == grantID {
			_ = os.Remove(d.path(dirTokens, name))
		}
	}
	return nil
}

// allGrants loads every grant. Caller holds the lock.
func (d *Disk) allGrants() ([]*Grant, error) {
	entries, err := os.ReadDir(filepath.Join(d.root, dirGrants))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []*Grant{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		g, err := d.loadGrant(strings.TrimSuffix(e.Name(), ".yaml"))
		if err != nil {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}
