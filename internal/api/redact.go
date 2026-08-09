package api

import (
	"sort"
	"strings"
)

// redactMark is what a secret value is replaced with in run output.
const redactMark = "(secret)"

// minRedactLen is the shortest value worth replacing.
//
// A redactor works by literal substring replacement, so a very short secret
// would match everywhere: a value of "1" or "dev" would turn the log into
// confetti and destroy exactly the diagnostic information the log exists for.
// Values that short are also not credentials in any meaningful sense — the risk
// they carry is far smaller than the damage blanket-replacing them would do.
const minRedactLen = 6

// redactor replaces known secret values in engine output.
//
// Why this is possible at all: the values are not guessed. A run resolves its
// own TF_VAR_* secrets and per-project environment variables before starting, so
// this is a literal replacement of strings we know we injected — not a pattern
// hunt for things that look like credentials.
//
// What it does NOT cover, and cannot: a credential the provider obtained by
// itself (an AWS profile read from ~/.aws, an instance role) is unknown here, so
// a provider error quoting one still reaches the log. This narrows the common
// case — ArcLoom-injected secrets echoed back in a provider's error message —
// rather than promising the log is secret-free.
type redactor struct{ values []string }

// newRedactor builds a redactor from the values a run injects. Values are
// sorted longest-first so that a secret containing another secret as a prefix is
// replaced whole rather than leaving a tail behind.
func newRedactor(sets ...map[string]string) *redactor {
	seen := map[string]bool{}
	var vals []string
	for _, set := range sets {
		for _, v := range set {
			if len(v) < minRedactLen || seen[v] {
				continue
			}
			seen[v] = true
			vals = append(vals, v)
		}
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	return &redactor{values: vals}
}

// line returns s with every known secret value replaced.
func (r *redactor) line(s string) string {
	if r == nil || len(r.values) == 0 {
		return s
	}
	for _, v := range r.values {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, redactMark)
		}
	}
	return s
}
