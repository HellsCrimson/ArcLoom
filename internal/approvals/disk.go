package approvals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned when an approval (or its plan artifact) is missing.
var ErrNotFound = errors.New("approval not found")

// keepPerProject caps retained approvals per project (oldest pruned on Create).
const keepPerProject = 50

// Disk stores, per approval:
//
//	<root>/<projectID>/<approvedUnixNano>-<id>.json    (metadata)
//	<root>/<projectID>/<approvedUnixNano>-<id>.tfplan  (saved plan artifact)
//
// The timestamp prefix makes lexical filename order equal chronological order.
// and, in a subdirectory, at most one stashed (not yet approved) plan per
// environment:
//
//	<root>/<projectID>/pending/<key>.json      (summary)
//	<root>/<projectID>/pending/<key>.tfplan    (saved plan artifact)
//
// The subdirectory keeps them out of the approval listing, which reads every
// .json in the project folder as an Approval record.
type Disk struct {
	root string
	mu   sync.Mutex
}

// NewDisk creates a disk-backed approval store rooted at dir.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create approvals dir: %w", err)
	}
	return &Disk{root: dir}, nil
}

func (d *Disk) projectDir(projectID string) string { return filepath.Join(d.root, projectID) }

// base returns the "<ts>-<id>" filename stem for an approval.
func base(a *Approval) string {
	return fmt.Sprintf("%020d-%s", a.ApprovedAt.UnixNano(), a.ID)
}

func (d *Disk) Create(a *Approval, planBytes []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(a.ProjectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stem := base(a)
	if err := os.WriteFile(filepath.Join(dir, stem+".tfplan"), planBytes, 0o644); err != nil {
		return fmt.Errorf("write plan artifact: %w", err)
	}
	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshal approval: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, stem+".json"), data, 0o644); err != nil {
		return err
	}
	d.pruneLocked(dir)
	return nil
}

func (d *Disk) Latest(projectID, env string) (*Approval, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(projectID)
	names, err := d.sortedJSON(dir)
	if err != nil {
		return nil, false
	}
	for i := len(names) - 1; i >= 0; i-- { // newest first
		a, err := d.read(filepath.Join(dir, names[i]))
		if err != nil {
			continue
		}
		if a.Env == env && !a.Consumed {
			return a, true
		}
	}
	return nil, false
}

func (d *Disk) Plan(projectID, id string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	stem, err := d.stemFor(projectID, id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(d.projectDir(projectID), stem+".tfplan"))
}

func (d *Disk) Consume(projectID, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(projectID)
	stem, err := d.stemFor(projectID, id)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, stem+".json")
	a, err := d.read(path)
	if err != nil {
		return err
	}
	a.Consumed = true
	a.ConsumedAt = time.Now()
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (d *Disk) InvalidateEnv(projectID, env string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(projectID)
	names, err := d.sortedJSON(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		a, err := d.read(path)
		if err != nil || a.Env != env || a.Consumed {
			continue
		}
		a.Consumed = true
		a.ConsumedAt = time.Now()
		if data, err := json.Marshal(a); err == nil {
			_ = os.WriteFile(path, data, 0o644)
		}
	}
	return nil
}

// pendingStem is the "<dir>/<key>" path stem of an environment's stashed plan.
// Environment names are safe path segments (letters, digits, _ and -) and so can
// never contain a dot: an "env.<name>" key can never collide with the default
// workspace's "default", not even for an environment literally named "default".
func (d *Disk) pendingStem(projectID, env string) string {
	key := "default"
	if env != "" {
		key = "env." + env
	}
	return filepath.Join(d.projectDir(projectID), "pending", key)
}

func (d *Disk) StashPlan(p *PendingPlan, planBytes []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	stem := d.pendingStem(p.ProjectID, p.Env)
	if err := os.MkdirAll(filepath.Dir(stem), 0o755); err != nil {
		return err
	}
	meta, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal pending plan: %w", err)
	}
	// The artifact is written (and renamed into place) before the summary that
	// advertises it, so a crash mid-stash leaves no summary pointing at a
	// truncated plan — the previous stash simply stands.
	if err := writeAtomic(stem+".tfplan", planBytes); err != nil {
		return fmt.Errorf("stash plan: %w", err)
	}
	if err := writeAtomic(stem+".json", meta); err != nil {
		return fmt.Errorf("stash plan summary: %w", err)
	}
	return nil
}

func (d *Disk) StashedPlan(projectID, env string) (*PendingPlan, []byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	stem := d.pendingStem(projectID, env)
	meta, err := os.ReadFile(stem + ".json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	var p PendingPlan
	if err := json.Unmarshal(meta, &p); err != nil {
		return nil, nil, fmt.Errorf("parse pending plan: %w", err)
	}
	planBytes, err := os.ReadFile(stem + ".tfplan")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	return &p, planBytes, nil
}

func (d *Disk) DropStash(projectID, env string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	stem := d.pendingStem(projectID, env)
	var firstErr error
	for _, path := range []string{stem + ".json", stem + ".tfplan"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// writeAtomic writes a file via a temporary sibling + rename, so a reader never
// sees a partially written plan artifact.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (d *Disk) List(projectID string) ([]*Approval, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := d.projectDir(projectID)
	names, err := d.sortedJSON(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*Approval, 0, len(names))
	for i := len(names) - 1; i >= 0; i-- { // newest first
		a, err := d.read(filepath.Join(dir, names[i]))
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (d *Disk) DeleteProject(projectID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return os.RemoveAll(d.projectDir(projectID))
}

// stemFor finds the "<ts>-<id>" filename stem for an approval id.
func (d *Disk) stemFor(projectID, id string) (string, error) {
	names, err := d.sortedJSON(d.projectDir(projectID))
	if err != nil {
		return "", ErrNotFound
	}
	suffix := "-" + id + ".json"
	for _, name := range names {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, ".json"), nil
		}
	}
	return "", ErrNotFound
}

// pruneLocked removes approvals (json + tfplan) beyond the retention cap.
func (d *Disk) pruneLocked(dir string) {
	names, err := d.sortedJSON(dir)
	if err != nil {
		return
	}
	for i := 0; i < len(names)-keepPerProject; i++ {
		stem := strings.TrimSuffix(names[i], ".json")
		_ = os.Remove(filepath.Join(dir, stem+".json"))
		_ = os.Remove(filepath.Join(dir, stem+".tfplan"))
	}
}

func (d *Disk) sortedJSON(dir string) ([]string, error) {
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

func (d *Disk) read(path string) (*Approval, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Approval
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return &a, nil
}
