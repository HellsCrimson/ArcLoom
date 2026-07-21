// Package audit records an append-only trail of who changed what: model edits
// (resources, providers, modules, backend, secrets, imports), git operations,
// project access-list changes, and administrative actions (user management,
// settings). It complements internal/runs, which already records deploy
// operations (plan/apply/destroy) per project.
//
// The trail is admin-visible and lives outside the project folders, so it is
// never committed to Git or captured in undo/redo snapshots.
package audit

import "time"

// Entry is one recorded action.
type Entry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	User      string    `json:"user"`   // username; empty when auth is disabled
	Action    string    `json:"action"` // friendly verb, e.g. "resource.upsert"
	ProjectID string    `json:"projectId,omitempty"`
	Detail    string    `json:"detail,omitempty"` // concrete path / params (never secret values)
}

// Filter narrows a List query. Zero-value fields are ignored; Limit<=0 returns
// all matching entries.
type Filter struct {
	ProjectID string
	User      string
	Limit     int
}

// Store persists audit entries. Implementations must be safe for concurrent use.
type Store interface {
	// Append records an action.
	Append(e *Entry) error
	// List returns entries newest first, honoring the filter.
	List(f Filter) ([]*Entry, error)
	// DeleteProject drops every entry tied to a project (on project delete).
	DeleteProject(projectID string) error
}
