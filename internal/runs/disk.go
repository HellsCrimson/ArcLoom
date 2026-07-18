package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ErrNotFound is returned when a run does not exist.
var ErrNotFound = errors.New("run not found")

// keepPerProject caps how many runs are retained per project; the oldest are
// pruned on Append.
const keepPerProject = 50

// Disk stores one JSON file per run:
//
//	<root>/<projectID>/<startedUnixNano>-<runID>.json
//
// The timestamp prefix makes lexical filename order equal chronological order.
type Disk struct {
	root string
	mu   sync.Mutex
}

// NewDisk creates a disk-backed run store rooted at dir.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create runs dir: %w", err)
	}
	return &Disk{root: dir}, nil
}

func (d *Disk) projectDir(projectID string) string { return filepath.Join(d.root, projectID) }

func (d *Disk) Append(r *Run) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(r.ProjectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}
	name := fmt.Sprintf("%020d-%s.json", r.StartedAt.UnixNano(), r.ID)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return err
	}
	// Prune beyond the retention cap (files sort chronologically by name).
	names, err := d.sortedNames(dir)
	if err != nil {
		return nil // history was written; pruning is best-effort
	}
	for i := 0; i < len(names)-keepPerProject; i++ {
		_ = os.Remove(filepath.Join(dir, names[i]))
	}
	return nil
}

func (d *Disk) List(projectID string) ([]*Run, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(projectID)
	names, err := d.sortedNames(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*Run, 0, len(names))
	for i := len(names) - 1; i >= 0; i-- { // newest first
		r, err := d.read(filepath.Join(dir, names[i]))
		if err != nil {
			continue // skip unreadable/foreign files
		}
		r.Log = ""
		out = append(out, r)
	}
	return out, nil
}

func (d *Disk) Get(projectID, id string) (*Run, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(projectID)
	names, err := d.sortedNames(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	for _, name := range names {
		if strings.HasSuffix(name, "-"+id+".json") {
			return d.read(filepath.Join(dir, name))
		}
	}
	return nil, ErrNotFound
}

func (d *Disk) DeleteProject(projectID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return os.RemoveAll(d.projectDir(projectID))
}

func (d *Disk) sortedNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (d *Disk) read(path string) (*Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return &r, nil
}
