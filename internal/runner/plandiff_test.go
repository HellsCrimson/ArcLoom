package runner

import (
	"encoding/json"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

// changeFromJSON decodes a plan-JSON "change" object the way tfjson does, so the
// tests exercise the same shapes (float64 numbers, []any, map[string]any).
func changeFromJSON(t *testing.T, s string) *tfjson.Change {
	t.Helper()
	var ch tfjson.Change
	if err := json.Unmarshal([]byte(s), &ch); err != nil {
		t.Fatalf("decode change: %v", err)
	}
	return &ch
}

func diffByPath(diffs []AttrDiff, path string) (AttrDiff, bool) {
	for _, d := range diffs {
		if d.Path == path {
			return d, true
		}
	}
	return AttrDiff{}, false
}

func TestAttributeDiffsScalarsAndUnchanged(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"name": "web", "size": 2, "on": true, "keep": "same"},
		"after":  {"name": "web2", "size": 4, "on": false, "keep": "same"}
	}`)
	diffs, truncated := attributeDiffs(ch, maxAttrDiffs)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(diffs) != 3 {
		t.Fatalf("want 3 diffs (keep is unchanged), got %d: %+v", len(diffs), diffs)
	}
	// Sorted by path.
	if diffs[0].Path != "name" || diffs[1].Path != "on" || diffs[2].Path != "size" {
		t.Fatalf("diffs not sorted by path: %+v", diffs)
	}
	name, _ := diffByPath(diffs, "name")
	if name.Action != "update" || name.Before != `"web"` || name.After != `"web2"` {
		t.Fatalf("name diff = %+v", name)
	}
	size, _ := diffByPath(diffs, "size")
	if size.Before != "2" || size.After != "4" {
		t.Fatalf("number formatting: %+v", size)
	}
	on, _ := diffByPath(diffs, "on")
	if on.Before != "true" || on.After != "false" {
		t.Fatalf("bool formatting: %+v", on)
	}
}

func TestAttributeDiffsNestedAndAddRemove(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"tags": {"Name": "a", "Gone": "x"}, "ports": [80, 443]},
		"after":  {"tags": {"Name": "b", "New": "y"}, "ports": [80, 8443, 9000]}
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)

	tagName, ok := diffByPath(diffs, "tags.Name")
	if !ok || tagName.Action != "update" {
		t.Fatalf("tags.Name = %+v (ok=%v)", tagName, ok)
	}
	gone, ok := diffByPath(diffs, "tags.Gone")
	if !ok || gone.Action != "remove" || gone.Before != `"x"` || gone.After != "" {
		t.Fatalf("removed key = %+v (ok=%v)", gone, ok)
	}
	added, ok := diffByPath(diffs, "tags.New")
	if !ok || added.Action != "add" || added.After != `"y"` || added.Before != "" {
		t.Fatalf("added key = %+v (ok=%v)", added, ok)
	}
	// ports[0] is unchanged, [1] updated, [2] appended.
	if _, ok := diffByPath(diffs, "ports[0]"); ok {
		t.Fatal("unchanged list element should not be reported")
	}
	p1, _ := diffByPath(diffs, "ports[1]")
	if p1.Action != "update" || p1.Before != "443" || p1.After != "8443" {
		t.Fatalf("ports[1] = %+v", p1)
	}
	p2, _ := diffByPath(diffs, "ports[2]")
	if p2.Action != "add" || p2.After != "9000" {
		t.Fatalf("ports[2] = %+v", p2)
	}
}

func TestAttributeDiffsUnknownAfterApply(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"arn": "arn:old", "id": "i-1"},
		"after":  {"id": "i-1"},
		"after_unknown": {"arn": true}
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	arn, ok := diffByPath(diffs, "arn")
	if !ok {
		t.Fatalf("arn missing: %+v", diffs)
	}
	if !arn.Unknown || arn.After != unknownAfterApply || arn.Before != `"arn:old"` {
		t.Fatalf("arn = %+v", arn)
	}
	if arn.Action != "update" {
		t.Fatalf("an unknown value over an existing one is an update, got %q", arn.Action)
	}
}

func TestAttributeDiffsSensitiveIsRedacted(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"password": "hunter2"},
		"after":  {"password": "correct-horse"},
		"before_sensitive": {"password": true},
		"after_sensitive":  {"password": true}
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff, got %+v", diffs)
	}
	d := diffs[0]
	if !d.Sensitive || d.Before != redactedValue || d.After != redactedValue {
		t.Fatalf("sensitive value leaked or unmarked: %+v", d)
	}
}

func TestAttributeDiffsSensitiveUnchangedIsSkipped(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"password": "same"},
		"after":  {"password": "same"},
		"before_sensitive": {"password": true},
		"after_sensitive":  {"password": true}
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	if len(diffs) != 0 {
		t.Fatalf("unchanged sensitive value should produce no row: %+v", diffs)
	}
}

// Moving a literal to a secret changes NO value — the engine plans an update
// only to re-record the marking in state ("this attribute value will be marked
// as sensitive … The value is unchanged"). This is the shape a real OpenTofu
// plan produces for that case, taken verbatim from one; without a row for it
// the plan panel showed "update" and listed nothing at all.
func TestAttributeDiffsSensitivityOnlyChange(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"id": "432445379711157715", "triggers": {"token": "abc"}},
		"after":  {"id": "432445379711157715", "triggers": {"token": "abc"}},
		"before_sensitive": {"triggers": {}},
		"after_sensitive":  {"triggers": {"token": true}}
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	if len(diffs) != 1 {
		t.Fatalf("want 1 row for the classification change, got %+v", diffs)
	}
	d := diffs[0]
	if d.Path != "triggers.token" || d.Action != "update" {
		t.Fatalf("wrong row: %+v", d)
	}
	if !d.SensitivityOnly || !d.Sensitive {
		t.Fatalf("row not marked as becoming sensitive: %+v", d)
	}
	// The value on both sides is exactly the one that just became a secret, so
	// neither side may carry it — this diff reaches the browser AND run history.
	if d.Before != "" || d.After != "" {
		t.Fatalf("a value leaked into a sensitivity-only row: %+v", d)
	}
}

// The reverse — a secret turned back into a literal — is the same row.
func TestAttributeDiffsSensitivityRemoved(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"token": "abc"},
		"after":  {"token": "abc"},
		"before_sensitive": {"token": true},
		"after_sensitive":  {}
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	if len(diffs) != 1 || !diffs[0].SensitivityOnly || diffs[0].Before != "" || diffs[0].After != "" {
		t.Fatalf("want one value-free classification row, got %+v", diffs)
	}
	// Sensitive carries the AFTER state, so this direction reads "no longer".
	if diffs[0].Sensitive {
		t.Fatalf("a row for losing sensitivity must not claim the value is sensitive: %+v", diffs[0])
	}
}

// Guard the neighbouring case: same value, same marking, still nothing.
func TestAttributeDiffsSensitivityUnchangedStillSkipped(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"token": "abc"},
		"after":  {"token": "abc"},
		"before_sensitive": {"token": true},
		"after_sensitive":  {"token": true}
	}`)
	if diffs, _ := attributeDiffs(ch, maxAttrDiffs); len(diffs) != 0 {
		t.Fatalf("no marking change and no value change must produce no row: %+v", diffs)
	}
}

func TestAttributeDiffsReplacePaths(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"ami": "ami-1", "tags": {"Name": "a"}, "root": {"size": 8}},
		"after":  {"ami": "ami-2", "tags": {"Name": "b"}, "root": {"size": 16}},
		"replace_paths": [["ami"], ["root"]]
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	ami, _ := diffByPath(diffs, "ami")
	if !ami.ForcesReplace {
		t.Fatalf("ami should force replacement: %+v", ami)
	}
	// A replace path on a block marks its children too.
	size, ok := diffByPath(diffs, "root.size")
	if !ok || !size.ForcesReplace {
		t.Fatalf("root.size should inherit forces-replace: %+v (ok=%v)", size, ok)
	}
	tag, _ := diffByPath(diffs, "tags.Name")
	if tag.ForcesReplace {
		t.Fatalf("tags.Name must not force replacement: %+v", tag)
	}
}

func TestAttributeDiffsIndexedReplacePath(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"disk": [{"size": 8}]},
		"after":  {"disk": [{"size": 16}]},
		"replace_paths": [["disk", 0, "size"]]
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	d, ok := diffByPath(diffs, "disk[0].size")
	if !ok || !d.ForcesReplace {
		t.Fatalf("disk[0].size = %+v (ok=%v)", d, ok)
	}
}

func TestAttributeDiffsBudgetTruncates(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"a": 1, "b": 1, "c": 1},
		"after":  {"a": 2, "b": 2, "c": 2}
	}`)
	diffs, truncated := attributeDiffs(ch, 2)
	if len(diffs) != 2 || !truncated {
		t.Fatalf("want 2 diffs + truncated, got %d truncated=%v", len(diffs), truncated)
	}
	// A spent plan-wide budget produces no rows — and must not claim truncation,
	// which would make the UI promise a longer list than exists.
	if diffs, truncated := attributeDiffs(ch, 0); len(diffs) != 0 || truncated {
		t.Fatalf("zero budget: got %d diffs truncated=%v", len(diffs), truncated)
	}
}

func TestAttributeDiffsEmptyStringVsNull(t *testing.T) {
	ch := changeFromJSON(t, `{
		"before": {"desc": null},
		"after":  {"desc": ""}
	}`)
	diffs, _ := attributeDiffs(ch, maxAttrDiffs)
	d, ok := diffByPath(diffs, "desc")
	if !ok || d.Action != "add" || d.After != `""` {
		t.Fatalf(`null -> "" should be an add of the empty string: %+v (ok=%v)`, d, ok)
	}
}

func TestAttributeDiffsNilChangeAndNoDiff(t *testing.T) {
	if diffs, tr := attributeDiffs(nil, maxAttrDiffs); diffs != nil || tr {
		t.Fatalf("nil change: %+v %v", diffs, tr)
	}
	ch := changeFromJSON(t, `{"before": {"a": 1}, "after": {"a": 1}}`)
	if diffs, _ := attributeDiffs(ch, maxAttrDiffs); len(diffs) != 0 {
		t.Fatalf("identical values: %+v", diffs)
	}
}
