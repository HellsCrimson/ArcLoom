package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// maxEntries caps how many entries are retained. compaction triggers once the
// live count exceeds maxEntries+slack, keeping appends cheap (append-only) in
// the common case and only rewriting occasionally.
const (
	maxEntries = 2000
	slack      = 500
)

// Disk is an append-only audit log stored as JSON-lines in a single file. Low
// write volume (human actions) makes a single file simpler than a directory
// tree; compaction keeps it bounded.
type Disk struct {
	path string
	mu   sync.Mutex
	n    int // live entry count (approximate between compactions)
}

// NewDisk opens (or creates) the audit log under dir.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	d := &Disk{path: filepath.Join(dir, "audit.log")}
	entries, err := d.readAll()
	if err != nil {
		return nil, err
	}
	d.n = len(entries)
	return d, nil
}

func (d *Disk) Append(e *Entry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	f, err := os.OpenFile(d.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	d.n++
	if d.n > maxEntries+slack {
		d.compact() // best-effort; the entry is already durable
	}
	return nil
}

func (d *Disk) List(f Filter) ([]*Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries, err := d.readAll()
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- { // newest first
		e := entries[i]
		if f.ProjectID != "" && e.ProjectID != f.ProjectID {
			continue
		}
		if f.User != "" && e.User != f.User {
			continue
		}
		out = append(out, e)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (d *Disk) DeleteProject(projectID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries, err := d.readAll()
	if err != nil {
		return err
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.ProjectID != projectID {
			kept = append(kept, e)
		}
	}
	return d.rewrite(kept)
}

// compact rewrites the file keeping only the newest maxEntries. Called under the
// lock; failure leaves the (larger) file intact.
func (d *Disk) compact() {
	entries, err := d.readAll()
	if err != nil {
		return
	}
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
	_ = d.rewrite(entries)
}

// rewrite atomically replaces the log with the given entries and refreshes n.
func (d *Disk) rewrite(entries []*Entry) error {
	tmp := d.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, d.path); err != nil {
		return err
	}
	d.n = len(entries)
	return nil
}

// readAll parses every entry in file order (chronological). A missing file is an
// empty log; malformed lines are skipped.
func (d *Disk) readAll() ([]*Entry, error) {
	f, err := os.Open(d.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []*Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		ec := e
		out = append(out, &ec)
	}
	return out, sc.Err()
}
