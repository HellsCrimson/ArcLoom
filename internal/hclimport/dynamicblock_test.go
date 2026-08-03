package hclimport

import (
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// A `dynamic` block is the construct that needs BOTH halves of a nested block:
// its label ("ingress") and its own nested `content` block. Importing only the
// top level's attributes kept the for_each and dropped both, so generation
// emitted a bare `dynamic {` — which the engine rejects outright with "Missing
// type for dynamic; All dynamic blocks must have 1 labels (type)" — with the
// content gone besides.
func TestImport_DynamicBlockKeepsLabelAndContent(t *testing.T) {
	src := `
resource "aws_security_group" "web" {
  name = "web"

  dynamic "ingress" {
    for_each = var.ports
    content {
      from_port = ingress.value
      protocol  = "tcp"
    }
  }
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(res.Resources))
	}
	dyn := res.Resources[0].Blocks["dynamic"]
	if len(dyn) != 1 {
		t.Fatalf("expected 1 dynamic block, got %d", len(dyn))
	}
	if got := dyn[0].Labels; len(got) != 1 || got[0] != "ingress" {
		t.Fatalf("dynamic labels = %v, want [ingress]", got)
	}
	if _, ok := dyn[0].Attrs["for_each"]; !ok {
		t.Errorf("dynamic block lost its for_each: %v", dyn[0].Attrs)
	}
	content := dyn[0].Blocks["content"]
	if len(content) != 1 {
		t.Fatalf("expected the content block to be imported, got %d", len(content))
	}
	for _, want := range []string{"from_port", "protocol"} {
		if _, ok := content[0].Attrs[want]; !ok {
			t.Errorf("content block lost %q: %v", want, content[0].Attrs)
		}
	}
}

// Labels are not a dynamic-block quirk: every labelled nested block lost its
// label the same way (`provisioner "local-exec"` became `provisioner {`), and
// everything below depth one was dropped whether labelled or not. Unlabelled
// one-level blocks (lifecycle) always worked and must keep working.
func TestImport_NestedBlockLabelsAndDepth(t *testing.T) {
	src := `
resource "null_resource" "r" {
  provisioner "local-exec" {
    command = "echo hi"
    environment {
      FOO = "bar"
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	blocks := res.Resources[0].Blocks
	prov := blocks["provisioner"]
	if len(prov) != 1 || len(prov[0].Labels) != 1 || prov[0].Labels[0] != "local-exec" {
		t.Fatalf("provisioner labels = %v, want [local-exec]", prov[0].Labels)
	}
	if len(prov[0].Blocks["environment"]) != 1 {
		t.Errorf("provisioner lost its nested environment block: %v", prov[0].Blocks)
	}
	life := blocks["lifecycle"]
	if len(life) != 1 || len(life[0].Labels) != 0 {
		t.Errorf("lifecycle must stay unlabelled, got labels %v", life[0].Labels)
	}
	if _, ok := life[0].Attrs["create_before_destroy"]; !ok {
		t.Errorf("lifecycle lost its attribute: %v", life[0].Attrs)
	}
}

// The end-to-end claim: import -> model -> generate reproduces a config the
// engine accepts, and a second import of that output is stable. Generation is
// what actually emitted the broken `dynamic {`, so asserting on the text is the
// point.
func TestRoundTrip_DynamicBlock(t *testing.T) {
	src := `
resource "aws_security_group" "web" {
  name = "web"

  dynamic "ingress" {
    for_each = var.ports
    content {
      from_port = ingress.value
      to_port   = ingress.value
      protocol  = "tcp"
    }
  }
}
`
	first, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Providers: first.Providers,
		Variables: []model.Variable{{Name: "ports", Type: "set(number)"}},
		Resources: first.Resources,
	}
	out, err := hclgen.Generate(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	gen := string(out)
	if !strings.Contains(gen, `dynamic "ingress" {`) {
		t.Fatalf("generated config lost the dynamic block's label:\n%s", gen)
	}
	if !strings.Contains(gen, "content {") || !strings.Contains(gen, "from_port") {
		t.Fatalf("generated config lost the content block:\n%s", gen)
	}

	// Re-importing the generated config must reach the same model, which is what
	// "round-trips" means for the store/preview/runner (all of which regenerate).
	second, err := Parse(map[string][]byte{"main.tf": out}, ids())
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	got := second.Resources[0].Blocks["dynamic"]
	if len(got) != 1 || len(got[0].Labels) != 1 || got[0].Labels[0] != "ingress" {
		t.Fatalf("label did not survive the round trip: %+v", got)
	}
	if len(got[0].Blocks["content"][0].Attrs) != 3 {
		t.Fatalf("content attributes did not survive: %+v", got[0].Blocks["content"])
	}
}
