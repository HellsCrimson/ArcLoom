package runner

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"

	tfjson "github.com/hashicorp/terraform-json"
)

// Attribute-level plan diffs. The streamed CLI log has always contained the
// per-attribute diff, but only as text a human has to scroll — nothing above
// this layer could tell *which field* a pending update touches. These helpers
// turn a plan's before/after object pair into a flat, path-addressed list the
// API and UI can annotate a resource form with.
const (
	// maxAttrDiffs caps one resource's diff rows.
	maxAttrDiffs = 60
	// maxPlanDiffs caps the whole plan's diff rows, so a large plan can't blow
	// up the SSE payload or a stored run record.
	maxPlanDiffs = 1500
	// maxDiffDepth stops the walk in pathological deeply-nested values.
	maxDiffDepth = 12
	// maxDiffValLen truncates a single rendered value.
	maxDiffValLen = 240
)

// unknownAfterApply is the placeholder for a value the provider only computes
// during apply (the plan's after_unknown).
const unknownAfterApply = "(known after apply)"

// redactedValue replaces both sides of a sensitive attribute. A sensitive value
// must never leave this function — the diff travels to the browser and is
// persisted in run history.
const redactedValue = "(sensitive)"

// AttrDiff is one attribute-level change inside a resource's planned change.
type AttrDiff struct {
	// Path is the attribute path relative to the resource, e.g. "tags.Name" or
	// "network_interface[0].subnet_id".
	Path string `json:"path"`
	// Action is add | update | remove for this attribute.
	Action string `json:"action"`
	// Before and After are rendered values (strings quoted, containers compact
	// JSON, both truncated). Empty means "not applicable" for the action.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Unknown marks a value only known after apply.
	Unknown bool `json:"unknown,omitempty"`
	// Sensitive marks a redacted attribute; Before/After carry no real value.
	Sensitive bool `json:"sensitive,omitempty"`
	// ForcesReplace marks an attribute listed in the plan's replace_paths — the
	// change that forces the resource to be recreated rather than updated.
	ForcesReplace bool `json:"forcesReplace,omitempty"`
}

// attributeDiffs flattens a plan change's before/after pair into per-attribute
// rows, honouring after_unknown (known after apply), before/after_sensitive
// (redacted) and replace_paths (forces replacement). The second return value
// reports that budget ran out and the list is partial.
//
// Only update/replace changes are worth diffing: a create has no "before" to
// compare against (the form already shows every value being set) and a delete
// removes the resource whole, so both would spend the budget on rows that say
// nothing the action badge doesn't.
func attributeDiffs(ch *tfjson.Change, budget int) ([]AttrDiff, bool) {
	if ch == nil || budget <= 0 {
		return nil, false
	}
	w := &diffWalker{replace: replacePathSet(ch.ReplacePaths), budget: budget}
	w.walk("", ch.Before, ch.After, ch.AfterUnknown, ch.BeforeSensitive, ch.AfterSensitive, 0)
	sort.Slice(w.out, func(i, j int) bool { return w.out[i].Path < w.out[j].Path })
	return w.out, w.truncated
}

type diffWalker struct {
	out       []AttrDiff
	replace   map[string]bool
	budget    int
	truncated bool
}

// walk compares one position in the before/after trees. unknown/bsens/asens are
// the parallel sub-trees from the plan JSON at the same position.
func (w *diffWalker) walk(path string, before, after, unknown, bsens, asens any, depth int) {
	if w.budget <= 0 {
		w.truncated = true
		return
	}
	// A sensitive value is never rendered, but the fact that it changes is
	// still worth reporting — so the diff row exists with redacted sides.
	if isTrue(bsens) || isTrue(asens) {
		if !sameValue(before, after) || isTrue(unknown) {
			w.emit(AttrDiff{
				Path: path, Action: actionFor(before, after, isTrue(unknown)),
				Before: redactedValue, After: redactedValue, Sensitive: true,
				Unknown: isTrue(unknown),
			})
		}
		return
	}
	// Unknown at this position: the whole subtree is computed during apply.
	if isTrue(unknown) {
		w.emit(AttrDiff{
			Path: path, Action: actionFor(before, after, true),
			Before: formatValue(before), After: unknownAfterApply, Unknown: true,
		})
		return
	}
	if before == nil && after == nil {
		return
	}
	bMap, bIsMap := before.(map[string]any)
	aMap, aIsMap := after.(map[string]any)
	if (bIsMap || aIsMap) && depth < maxDiffDepth {
		for _, k := range unionKeys(bMap, aMap) {
			w.walk(childPath(path, k), bMap[k], aMap[k],
				childOf(unknown, k), childOf(bsens, k), childOf(asens, k), depth+1)
		}
		return
	}
	bArr, bIsArr := before.([]any)
	aArr, aIsArr := after.([]any)
	if (bIsArr || aIsArr) && depth < maxDiffDepth {
		n := max(len(bArr), len(aArr))
		for i := range n {
			w.walk(path+"["+strconv.Itoa(i)+"]", at(bArr, i), at(aArr, i),
				indexOf(unknown, i), indexOf(bsens, i), indexOf(asens, i), depth+1)
		}
		return
	}
	if sameValue(before, after) {
		return
	}
	w.emit(AttrDiff{
		Path: path, Action: actionFor(before, after, false),
		Before: formatValue(before), After: formatValue(after),
	})
}

func (w *diffWalker) emit(d AttrDiff) {
	if d.Path == "" { // the resource root itself is never a diff row
		return
	}
	if w.budget <= 0 {
		w.truncated = true
		return
	}
	d.ForcesReplace = w.forcesReplace(d.Path)
	// A removal has no "after" and an addition no "before"; drop the "null"
	// placeholder so the UI renders a single value instead of "null → x".
	switch d.Action {
	case "add":
		d.Before = ""
	case "remove":
		if !d.Unknown {
			d.After = ""
		}
	}
	w.out = append(w.out, d)
	w.budget--
}

// forcesReplace reports whether path is (or is inside) a replace_paths entry.
func (w *diffWalker) forcesReplace(path string) bool {
	if w.replace[path] {
		return true
	}
	for p := range w.replace {
		if len(path) > len(p) && path[:len(p)] == p &&
			(path[len(p)] == '.' || path[len(p)] == '[') {
			return true
		}
	}
	return false
}

// replacePathSet renders the plan's replace_paths (each a list of string keys /
// numeric indexes) as the same path strings the walk produces.
func replacePathSet(paths []any) map[string]bool {
	if len(paths) == 0 {
		return nil
	}
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		steps, ok := p.([]any)
		if !ok {
			continue
		}
		cur := ""
		for _, s := range steps {
			switch v := s.(type) {
			case string:
				cur = childPath(cur, v)
			case float64:
				cur += "[" + strconv.Itoa(int(v)) + "]"
			case json.Number:
				cur += "[" + v.String() + "]"
			}
		}
		if cur != "" {
			set[cur] = true
		}
	}
	return set
}

// actionFor classifies a change from the presence of each side. An unknown
// after-value always counts as a value being produced.
func actionFor(before, after any, unknown bool) string {
	switch {
	case before == nil && (after != nil || unknown):
		return "add"
	case after == nil && !unknown:
		if before == nil {
			return "update"
		}
		return "remove"
	default:
		return "update"
	}
}

// formatValue renders a JSON-decoded plan value for display: strings quoted
// (so "" is distinguishable from unset), containers as compact JSON, everything
// truncated.
func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return truncateValue(strconv.Quote(t))
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "(unrenderable)"
		}
		return truncateValue(string(b))
	}
}

func truncateValue(s string) string {
	if len(s) <= maxDiffValLen {
		return s
	}
	return s[:maxDiffValLen] + "…"
}

// childPath joins an object key onto a path ("" at the resource root).
func childPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// childOf indexes a parallel unknown/sensitive sub-tree by object key.
func childOf(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

// indexOf indexes a parallel unknown/sensitive sub-tree by list position.
func indexOf(v any, i int) any {
	if a, ok := v.([]any); ok {
		return at(a, i)
	}
	return nil
}

func at(a []any, i int) any {
	if i < len(a) {
		return a[i]
	}
	return nil
}

func isTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

// sameValue compares two JSON-decoded values. reflect.DeepEqual is exact for
// the types encoding/json produces (nil, bool, float64, string, []any, map).
func sameValue(a, b any) bool { return reflect.DeepEqual(a, b) }

// unionKeys returns every key of both objects, sorted, so output is stable.
func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
