package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
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

// File modes. The audit log records who did what, and its entries quote request
// paths — it is the security record, so it gets the same treatment as the
// credential stores (internal/secrets, session, apikey, oauth) rather than the
// world-readable default it had. Matters on a bare-metal or VM install with
// more than one local account; latent inside the single-user container.
const (
	fileMode = 0o600
	dirMode  = 0o700
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
	if err := os.MkdirAll(dir, dirMode); err != nil {
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
	f, err := os.OpenFile(d.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
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

// compact moves the oldest entries out of the live log into a dated archive
// file, keeping only the newest maxEntries active. Called under the lock;
// failure leaves the (larger) file intact.
//
// The entries are ARCHIVED, not deleted. Compaction used to drop them, which
// made the log self-erasing under load: any user holding a mutating permission
// could generate audited events at HTTP speed — none of these routes is
// rate-limited — and roughly 2,500 cheap requests would push every earlier
// entry off the end, including the record of whatever they had just done. A log
// an attacker can rotate away in a few seconds is not evidence of anything.
//
// Archives are plain JSONL, same format as the live log, one file per UTC day
// (`audit-YYYYMMDD.log`). They are deliberately NOT read by List: the UI stays
// fast and bounded, and the history is on disk for whoever needs it — `cat`,
// `jq`, or a log shipper. Nothing prunes them; a retention policy is the
// operator's to set with ordinary tooling, and an ArcLoom-side "delete old
// audit" would be the erasure primitive this exists to remove.
func (d *Disk) compact() {
	entries, err := d.readAll()
	if err != nil || len(entries) <= maxEntries {
		return
	}
	cut := len(entries) - maxEntries
	older, keep := entries[:cut], entries[cut:]
	if err := d.archive(older); err != nil {
		// Truncating what could not be archived would be the very data loss this
		// change exists to prevent. Leave the log long and try again next append.
		slog.Error("audit: could not archive older entries; keeping them in the live log", "err", err)
		return
	}
	_ = d.rewrite(keep)
}

// archive appends entries to per-day archive files. Grouping by the entry's own
// UTC day (rather than by rotation time) keeps a day's history in one place no
// matter how many rotations it took to get there, and makes the file name mean
// something to whoever goes looking.
func (d *Disk) archive(entries []*Entry) error {
	byDay := map[string][]*Entry{}
	var order []string
	for _, e := range entries {
		day := e.Time.UTC().Format("20060102")
		if _, seen := byDay[day]; !seen {
			order = append(order, day)
		}
		byDay[day] = append(byDay[day], e)
	}
	for _, day := range order {
		path := filepath.Join(filepath.Dir(d.path), "audit-"+day+".log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		for _, e := range byDay[day] {
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
	}
	return nil
}

// rewrite atomically replaces the log with the given entries and refreshes n.
func (d *Disk) rewrite(entries []*Entry) error {
	tmp := d.path + ".tmp"
	// OpenFile rather than Create: Create is 0o666-before-umask, which would
	// undo the mode on every rewrite.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileMode)
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
