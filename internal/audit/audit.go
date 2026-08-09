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

// Origin says which surface an action came through. "Who did it" is not the
// whole story once non-browser credentials exist: the same account can act from
// the UI, from a script holding an API key, and from an AI agent over MCP, and
// those are worth telling apart when reading the trail back.
const (
	OriginUI  = "ui"  // browser session cookie
	OriginAPI = "api" // API key presented directly (CI, script, curl)
	OriginMCP = "mcp" // API key, but via an MCP tool call
)

// Entry is one recorded action.
type Entry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	User      string    `json:"user"`   // username; empty when auth is disabled
	Action    string    `json:"action"` // friendly verb, e.g. "resource.upsert"
	ProjectID string    `json:"projectId,omitempty"`
	Detail    string    `json:"detail,omitempty"` // concrete path / params (never secret values)
	// Via is the Origin* the action arrived through. Empty on entries written
	// before this field existed, which is why it is omitempty and the UI treats
	// an empty value as "unknown" rather than assuming the UI.
	Via string `json:"via,omitempty"`
	// KeyLabel names the API key used, for the two key-backed origins. The id is
	// kept alongside it because labels are user-chosen and need not be unique —
	// the label is what a human reads, the id is what they revoke.
	KeyLabel string `json:"keyLabel,omitempty"`
	KeyID    string `json:"keyId,omitempty"`
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
