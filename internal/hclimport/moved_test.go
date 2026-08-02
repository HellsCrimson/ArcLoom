package hclimport

import (
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// A `moved` block in an imported config must survive as one. Losing it is worse
// than losing an import block: a moved block that stops being generated makes
// the next apply DESTROY and recreate the objects it was there to rename.
func TestParse_MovedBlocks(t *testing.T) {
	src := `
moved {
  from = null_resource.www
  to   = null_resource.sub["www"]
}

moved {
  from = module.old.null_resource.a
  to   = module.team["alpha"].null_resource.a[0]
}
`
	n := 0
	res, err := Parse(map[string][]byte{"moved.tf": []byte(src)}, func() string {
		n++
		return "id" + string(rune('0'+n))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 2 {
		t.Fatalf("want 2 moved blocks, got %d: %+v", len(res.Moved), res.Moved)
	}
	a, b := res.Moved[0], res.Moved[1]
	if a.From != "null_resource.www" || a.To != `null_resource.sub["www"]` {
		t.Errorf("first block: %+v", a)
	}
	if b.From != "module.old.null_resource.a" || b.To != `module.team["alpha"].null_resource.a[0]` {
		t.Errorf("second block: %+v", b)
	}
	if a.ID == "" || b.ID == "" || a.ID == b.ID {
		t.Errorf("each block needs its own id: %q %q", a.ID, b.ID)
	}
}

// Generate -> parse -> generate: the addresses come back byte-identical, which
// is what lets a project with moved blocks be exported and re-imported.
func TestMovedBlocks_RoundTrip(t *testing.T) {
	p := &model.Project{
		Moved: []model.Moved{
			{ID: "1", From: "null_resource.www", To: `null_resource.sub["www"]`},
		},
	}
	out, err := hclgen.GenerateMoved(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(map[string][]byte{"moved.tf": out}, func() string { return "x" })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 1 {
		t.Fatalf("want 1 moved block, got %d", len(res.Moved))
	}
	got := res.Moved[0]
	if got.From != p.Moved[0].From || got.To != p.Moved[0].To {
		t.Fatalf("round trip changed the block: %+v", got)
	}
	again, err := hclgen.GenerateMoved(&model.Project{Moved: res.Moved})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(again)) != strings.TrimSpace(string(out)) {
		t.Errorf("re-generated HCL differs:\n%s\n---\n%s", out, again)
	}
}
