package user

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Disk is a Store that keeps one YAML file per user under root:
//
//	<root>/<id>.yaml
//
// Files are written 0o600 (dir 0o700) since they hold password hashes.
type Disk struct {
	root string
	mu   sync.Mutex
}

// NewDisk creates a disk-backed user store rooted at dir.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create users dir: %w", err)
	}
	return &Disk{root: dir}, nil
}

func (d *Disk) path(id string) string { return filepath.Join(d.root, id+".yaml") }

func (d *Disk) Create(u *User) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, _ := d.findByUsername(u.Username); existing != nil {
		return ErrExists
	}
	return d.persist(u)
}

func (d *Disk) Get(id string) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load(id)
}

func (d *Disk) ByUsername(username string) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	u, err := d.findByUsername(username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	return u, nil
}

func (d *Disk) BySubject(provider, subject string) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	users, err := d.all()
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Provider == provider && u.Subject == subject && subject != "" {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (d *Disk) List() ([]*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.all()
}

func (d *Disk) Update(id string, fn func(*User) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	u, err := d.load(id)
	if err != nil {
		return err
	}
	if err := fn(u); err != nil {
		return err
	}
	return d.persist(u)
}

func (d *Disk) Delete(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := os.Stat(d.path(id)); err != nil {
		return ErrNotFound
	}
	return os.Remove(d.path(id))
}

func (d *Disk) persist(u *User) error {
	data, err := yaml.Marshal(u)
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}
	return os.WriteFile(d.path(u.ID), data, 0o600)
}

func (d *Disk) load(id string) (*User, error) {
	data, err := os.ReadFile(d.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var u User
	if err := yaml.Unmarshal(data, &u); err != nil {
		return nil, fmt.Errorf("parse user %s: %w", id, err)
	}
	return &u, nil
}

// all reads every user file. Caller holds the lock.
func (d *Disk) all() ([]*User, error) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return nil, err
	}
	var out []*User
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yaml")
		u, err := d.load(id)
		if err != nil {
			continue // skip unreadable/foreign files
		}
		out = append(out, u)
	}
	return out, nil
}

// findByUsername returns the matching user or (nil, nil) if none. Caller holds
// the lock.
func (d *Disk) findByUsername(username string) (*User, error) {
	users, err := d.all()
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, nil
}
