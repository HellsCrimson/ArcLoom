// Package store holds project state behind a small interface so an in-memory
// implementation (tests) and a disk-backed one (production) are interchangeable.
package store

import (
	"errors"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// ErrNotFound is returned when a project does not exist.
var ErrNotFound = errors.New("not found")

// Store persists projects.
type Store interface {
	// Create stores a new project.
	Create(p *model.Project) error
	// Get returns a project by id.
	Get(id string) (*model.Project, error)
	// List returns all projects (order unspecified).
	List() ([]*model.Project, error)
	// Update runs fn against the stored project under a write lock and persists
	// any mutations fn makes.
	Update(id string, fn func(*model.Project) error) error
	// Delete removes a project.
	Delete(id string) error
}
