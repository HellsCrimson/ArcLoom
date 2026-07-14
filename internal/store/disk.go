package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

const configFile = "arcloom.yaml"

// Generator renders a project to a set of files keyed by path relative to the
// generated/ directory. Injected so the store stays independent of hclgen.
type Generator func(*model.Project) (map[string][]byte, error)

// Disk is a Store that keeps one folder per project under root:
//
//	<root>/<id>/arcloom.yaml     (the model)
//	<root>/<id>/generated/main.tf (regenerated on every save)
//
// State files are never written here; they stay in the CLI workspaces.
type Disk struct {
	root string
	gen  Generator
	mu   sync.Mutex
}

// NewDisk creates a disk-backed store rooted at dir.
func NewDisk(dir string, gen Generator) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return &Disk{root: dir, gen: gen}, nil
}

func (d *Disk) dir(id string) string { return filepath.Join(d.root, id) }

func (d *Disk) Create(p *model.Project) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.persist(p)
}

func (d *Disk) Get(id string) (*model.Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load(id)
}

func (d *Disk) List() ([]*model.Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return nil, err
	}
	var out []*model.Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := d.load(e.Name())
		if err != nil {
			continue // skip unreadable/foreign dirs
		}
		out = append(out, p)
	}
	return out, nil
}

func (d *Disk) Update(id string, fn func(*model.Project) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, err := d.load(id)
	if err != nil {
		return err
	}
	if err := fn(p); err != nil {
		return err
	}
	return d.persist(p)
}

func (d *Disk) Delete(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := os.Stat(d.dir(id)); err != nil {
		return ErrNotFound
	}
	return os.RemoveAll(d.dir(id))
}

// persist writes arcloom.yaml and regenerates generated/main.tf.
func (d *Disk) persist(p *model.Project) error {
	dir := d.dir(p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal project: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), data, 0o644); err != nil {
		return err
	}
	// Regenerate the .tf so the folder is a usable Terraform/OpenTofu project.
	// The generated/ dir is rebuilt each time so removed modules leave no stale
	// files.
	if d.gen != nil {
		files, err := d.gen(p)
		if err == nil {
			genDir := filepath.Join(dir, "generated")
			os.RemoveAll(genDir)
			for rel, content := range files {
				full := filepath.Join(genDir, rel)
				if os.MkdirAll(filepath.Dir(full), 0o755) == nil {
					_ = os.WriteFile(full, content, 0o644)
				}
			}
		}
	}
	return nil
}

func (d *Disk) load(id string) (*model.Project, error) {
	data, err := os.ReadFile(filepath.Join(d.dir(id), configFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var p model.Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configFile, err)
	}
	return &p, nil
}
