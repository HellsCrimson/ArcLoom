package session

import (
	"sync"
	"time"
)

// Memory is a concurrency-safe in-memory Store (used in tests).
type Memory struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewMemory() *Memory {
	return &Memory{sessions: map[string]*Session{}}
}

func (m *Memory) Create(userID, provider string, ttl time.Duration) (*Session, error) {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[token] = s
	return s, nil
}

func (m *Memory) Get(token string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok {
		return nil, ErrNotFound
	}
	if time.Now().After(s.ExpiresAt) {
		delete(m.sessions, token)
		return nil, ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *Memory) Delete(token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

func (m *Memory) DeleteByUser(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for token, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, token)
		}
	}
	return nil
}

func (m *Memory) GC() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for token, s := range m.sessions {
		if now.After(s.ExpiresAt) {
			delete(m.sessions, token)
		}
	}
	return nil
}
