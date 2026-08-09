package apikey

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Disk is a Store that keeps one YAML file per key under root:
//
//	<root>/<id>.yaml
//
// Files are 0o600 (dir 0o700). The file holds the secret's hash, never the
// secret.
type Disk struct {
	root string
	mu   sync.Mutex
}

// NewDisk creates a disk-backed key store rooted at dir.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create api keys dir: %w", err)
	}
	return &Disk{root: dir}, nil
}

func (d *Disk) path(id string) string { return filepath.Join(d.root, id+".yaml") }

func (d *Disk) Create(k *Key) (string, error) {
	token, err := prepare(k)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.write(k); err != nil {
		return "", err
	}
	return token, nil
}

func (d *Disk) Get(id string) (*Key, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load(id)
}

func (d *Disk) Verify(token string) (*Key, error) {
	id, secret, ok := ParseToken(token)
	if !ok {
		return nil, ErrInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	k, err := d.load(id)
	if err != nil {
		return nil, ErrInvalid
	}
	if !matches(k.Hash, secret) || !k.Usable(time.Now()) {
		return nil, ErrInvalid
	}
	return k, nil
}

func (d *Disk) List() ([]*Key, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.all(func(*Key) bool { return true })
}

func (d *Disk) ListByUser(userID string) ([]*Key, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.all(func(k *Key) bool { return k.UserID == userID })
}

func (d *Disk) Delete(id string) error {
	if !validID(id) {
		return nil // nothing a forged id could have created
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	err := os.Remove(d.path(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *Disk) DeleteByUser(userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	keys, err := d.all(func(k *Key) bool { return k.UserID == userID })
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := os.Remove(d.path(k.ID)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (d *Disk) Touch(id string, at time.Time) error {
	if !validID(id) {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	k, err := d.load(id)
	if err != nil {
		return err
	}
	k.LastUsedAt = &at
	return d.write(k)
}

// write persists a key. Caller holds the lock.
func (d *Disk) write(k *Key) error {
	data, err := yaml.Marshal(k)
	if err != nil {
		return err
	}
	return os.WriteFile(d.path(k.ID), data, 0o600)
}

// load reads one key. Caller holds the lock.
func (d *Disk) load(id string) (*Key, error) {
	data, err := os.ReadFile(d.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var k Key
	if err := yaml.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("parse api key: %w", err)
	}
	k.ID = id
	return &k, nil
}

// all returns every stored key matching keep. Caller holds the lock.
func (d *Disk) all(keep func(*Key) bool) ([]*Key, error) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Key
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yaml")
		k, err := d.load(id)
		if err != nil {
			continue // skip unreadable entries rather than failing the listing
		}
		if keep(k) {
			out = append(out, k)
		}
	}
	return out, nil
}
