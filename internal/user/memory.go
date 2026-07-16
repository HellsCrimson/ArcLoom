package user

import "sync"

// Memory is a concurrency-safe in-memory Store (used in tests).
type Memory struct {
	mu    sync.RWMutex
	users map[string]*User
}

func NewMemory() *Memory {
	return &Memory{users: map[string]*User{}}
}

func (m *Memory) Create(u *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.users {
		if e.Username == u.Username {
			return ErrExists
		}
	}
	cp := *u
	m.users[u.ID] = &cp
	return nil
}

func (m *Memory) Get(id string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *Memory) ByUsername(username string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.Username == username {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) BySubject(provider, subject string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.Provider == provider && u.Subject == subject && subject != "" {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) List() ([]*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		cp := *u
		out = append(out, &cp)
	}
	return out, nil
}

func (m *Memory) Update(id string, fn func(*User) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	return fn(u)
}

func (m *Memory) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[id]; !ok {
		return ErrNotFound
	}
	delete(m.users, id)
	return nil
}
