package store

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func sampleProject(id string) *model.Project {
	return &model.Project{
		ID:     id,
		Name:   "demo",
		Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "random", Source: "hashicorp/random"},
		},
		Resources: []model.Resource{{
			ID:   "res-1",
			Type: "random_password",
			Name: "db",
			Attrs: map[string]model.AttrValue{
				"length":  {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(24)},
				"special": {Mode: model.ModeLiteral, Literal: cty.True},
				"keepers": {Mode: model.ModeReference, Ref: "random_pet.p.keepers"},
			},
		}},
	}
}

func TestDisk_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDisk(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Create(sampleProject("p1")); err != nil {
		t.Fatal(err)
	}

	// Fresh store instance forces a reload from disk (no in-memory shortcut).
	d2, _ := NewDisk(dir, nil)
	got, err := d2.Get("p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "demo" || got.Engine != model.EngineTofu {
		t.Errorf("project metadata lost: %+v", got)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(got.Resources))
	}
	attrs := got.Resources[0].Attrs

	// Literal number survives with its cty type.
	length := attrs["length"]
	if length.Mode != model.ModeLiteral || length.Literal.AsBigFloat().Cmp(cty.NumberIntVal(24).AsBigFloat()) != 0 {
		t.Errorf("length literal not preserved: %+v", length)
	}
	// Literal bool survives.
	if sp := attrs["special"]; sp.Mode != model.ModeLiteral || sp.Literal != cty.True {
		t.Errorf("special literal not preserved: %+v", sp)
	}
	// Reference survives.
	if k := attrs["keepers"]; k.Mode != model.ModeReference || k.Ref != "random_pet.p.keepers" {
		t.Errorf("keepers reference not preserved: %+v", k)
	}
}

func TestDisk_ListAndDelete(t *testing.T) {
	dir := t.TempDir()
	d, _ := NewDisk(dir, nil)
	d.Create(sampleProject("a"))
	d.Create(sampleProject("b"))

	list, err := d.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(list))
	}

	if err := d.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get("a"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
	if list, _ := d.List(); len(list) != 1 {
		t.Errorf("expected 1 project after delete, got %d", len(list))
	}
}
