package apikey

import (
	"sync"
	"time"
)

// Memory is a concurrency-safe in-memory Store (used in tests).
type Memory struct {
	mu   sync.Mutex
	keys map[string]*Key
}

func NewMemory() *Memory { return &Memory{keys: map[string]*Key{}} }

func (m *Memory) Create(k *Key) (string, error) {
	token, err := prepare(k)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *k
	m.keys[k.ID] = &cp
	return token, nil
}

func (m *Memory) Get(id string) (*Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *k
	return &cp, nil
}

func (m *Memory) Verify(token string) (*Key, error) {
	id, secret, ok := ParseToken(token)
	if !ok {
		return nil, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k, found := m.keys[id]
	if !found || !matches(k.Hash, secret) || !k.Usable(time.Now()) {
		return nil, ErrInvalid
	}
	cp := *k
	return &cp, nil
}

func (m *Memory) List() ([]*Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filter(func(*Key) bool { return true }), nil
}

func (m *Memory) ListByUser(userID string) ([]*Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.filter(func(k *Key) bool { return k.UserID == userID }), nil
}

func (m *Memory) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, id)
	return nil
}

func (m *Memory) DeleteByUser(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, k := range m.keys {
		if k.UserID == userID {
			delete(m.keys, id)
		}
	}
	return nil
}

func (m *Memory) Touch(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k, ok := m.keys[id]; ok {
		k.LastUsedAt = &at
	}
	return nil
}

// filter returns copies of the keys matching keep. Caller holds the lock.
func (m *Memory) filter(keep func(*Key) bool) []*Key {
	var out []*Key
	for _, k := range m.keys {
		if keep(k) {
			cp := *k
			out = append(out, &cp)
		}
	}
	return out
}
