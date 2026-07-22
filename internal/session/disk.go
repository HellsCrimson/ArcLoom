package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Disk is a Store that keeps one YAML file per session token under root:
//
//	<root>/<token>.yaml
//
// Tokens are URL-safe base64 (filename-safe). Files are 0o600 (dir 0o700).
// Expired sessions are deleted lazily on Get and in bulk by GC.
type Disk struct {
	root string
	mu   sync.Mutex
}

// NewDisk creates a disk-backed session store rooted at dir.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}
	return &Disk{root: dir}, nil
}

func (d *Disk) path(token string) string { return filepath.Join(d.root, token+".yaml") }

// validToken reports whether a token is safe to use as a filename: the
// base64url alphabet only (matching newToken). Rejecting '.', '/' and '\'
// stops a forged cookie value from escaping the sessions directory when it is
// joined into a path.
func validToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func (d *Disk) Create(userID, provider string, ttl time.Duration) (*Session, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s := &Session{
		Token:     token,
		UserID:    userID,
		Provider:  provider,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := yaml.Marshal(s)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(d.path(token), data, 0o600); err != nil {
		return nil, err
	}
	return s, nil
}

func (d *Disk) Get(token string) (*Session, error) {
	if !validToken(token) {
		return nil, ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.load(token)
	if err != nil {
		return nil, err
	}
	if time.Now().After(s.ExpiresAt) {
		_ = os.Remove(d.path(token)) // lazy expiry
		return nil, ErrNotFound
	}
	return s, nil
}

func (d *Disk) Delete(token string) error {
	if !validToken(token) {
		return nil // nothing a forged/malformed token could have created
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	err := os.Remove(d.path(token))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *Disk) DeleteByUser(userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.forEach(func(token string, s *Session) {
		if s.UserID == userID {
			_ = os.Remove(d.path(token))
		}
	})
}

func (d *Disk) GC() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	return d.forEach(func(token string, s *Session) {
		if now.After(s.ExpiresAt) {
			_ = os.Remove(d.path(token))
		}
	})
}

func (d *Disk) load(token string) (*Session, error) {
	data, err := os.ReadFile(d.path(token))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var s Session
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	s.Token = token
	return &s, nil
}

// forEach iterates over every session file, calling fn. Caller holds the lock.
func (d *Disk) forEach(fn func(token string, s *Session)) error {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		token := strings.TrimSuffix(e.Name(), ".yaml")
		s, err := d.load(token)
		if err != nil {
			continue
		}
		fn(token, s)
	}
	return nil
}
