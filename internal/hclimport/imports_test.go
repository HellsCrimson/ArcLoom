package hclimport

import (
	"testing"
)

// An `import` block in an imported config must survive as one — losing it would
// silently drop an adoption the user wrote down, and (since it generates back
// into imports.tf) the next apply would simply not perform it.
func TestParse_ImportBlocks(t *testing.T) {
	src := `
import {
  to = null_resource.one
  id = "abc123"
}

import {
  for_each = toset(local.subdomains)
  to       = null_resource.sub[each.key]
  id       = each.value
  provider = null.other
}
`
	n := 0
	res, err := Parse(map[string][]byte{"imports.tf": []byte(src)}, func() string {
		n++
		return "id" + string(rune('0'+n))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imports) != 2 {
		t.Fatalf("want 2 import blocks, got %d: %+v", len(res.Imports), res.Imports)
	}
	one, many := res.Imports[0], res.Imports[1]
	if one.To != "null_resource.one" || one.ImportID != `"abc123"` || one.ForEach != "" {
		t.Errorf("simple block: %+v", one)
	}
	if many.ForEach != "toset(local.subdomains)" ||
		many.To != "null_resource.sub[each.key]" ||
		many.ImportID != "each.value" ||
		many.Provider != "null.other" {
		t.Errorf("for_each block: %+v", many)
	}
	if one.ID == "" || many.ID == "" || one.ID == many.ID {
		t.Errorf("each block needs its own id: %q %q", one.ID, many.ID)
	}
}
