package store

import (
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// Two calls to one local module must survive the disk round trip: the YAML
// write, the re-read, and Normalize (which synthesizes calls for v1 projects
// and must not touch a project that already has them).
func TestDisk_TwoCallsSameLocalModule(t *testing.T) {
	d, err := NewDisk(t.TempDir(), hclgen.GenerateFiles)
	if err != nil {
		t.Fatal(err)
	}
	p := &model.Project{
		ID: "p1", Name: "x", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "team"}},
		Modules: []model.Module{
			{Name: "team_a", Source: "./modules/team"},
			{Name: "team_b", Source: "./modules/team"},
		},
	}
	if err := d.Create(p); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Modules) != 2 {
		t.Fatalf("after reload: want 2 calls, got %d: %+v", len(got.Modules), got.Modules)
	}
	if len(got.LocalModules) != 1 {
		t.Fatalf("want 1 definition, got %d", len(got.LocalModules))
	}

	// A v1 project (no ModelVersion) with the same shape: the migration must not
	// invent a third call or drop either existing one.
	legacy := &model.Project{
		ID: "p2", Name: "y", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "team"}},
		Modules: []model.Module{
			{Name: "team_a", Source: "./modules/team"},
			{Name: "team_b", Source: "./modules/team"},
		},
	}
	legacy.Normalize()
	if len(legacy.Modules) != 2 {
		t.Fatalf("v1 migration changed the call list: %+v", legacy.Modules)
	}
}
