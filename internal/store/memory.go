package store

import (
	"sync"

	"github.com/matthiasrauline/arcloom/internal/model"
)

// Memory is a concurrency-safe in-memory Store (used in tests).
type Memory struct {
	mu       sync.RWMutex
	projects map[string]*model.Project
}

func NewMemory() *Memory {
	return &Memory{projects: map[string]*model.Project{}}
}

func (m *Memory) Create(p *model.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.projects[p.ID] = p
	return nil
}

func (m *Memory) Get(id string) (*model.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.projects[id]
	if !ok {
		return nil, ErrNotFound
	}
	return p, nil
}

func (m *Memory) List() ([]*model.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.Project, 0, len(m.projects))
	for _, p := range m.projects {
		out = append(out, p)
	}
	return out, nil
}

func (m *Memory) Update(id string, fn func(*model.Project) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return ErrNotFound
	}
	return fn(p)
}

func (m *Memory) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[id]; !ok {
		return ErrNotFound
	}
	delete(m.projects, id)
	return nil
}
