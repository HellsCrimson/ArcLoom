package runner

import "testing"

// Current conftest reports successes as a count; failures and warnings are
// separated and carry the query that produced them.
func TestParseConftest(t *testing.T) {
	out := `[
	  {"filename":"plan.json","namespace":"main","successes":2,
	   "warnings":[{"msg":"tag missing","metadata":{"query":"data.main.warn"}}],
	   "failures":[{"msg":"public bucket","metadata":{"query":"data.main.deny"}}]},
	  {"filename":"plan.json","namespace":"cost","successes":1,"failures":[{"msg":"too expensive"}]}
	]`
	res, err := parseConftest([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if res.FailureCount != 2 || res.WarningCount != 1 || res.Successes != 3 {
		t.Fatalf("counts = %d failures / %d warnings / %d successes", res.FailureCount, res.WarningCount, res.Successes)
	}
	if len(res.Violations) != 3 {
		t.Fatalf("violations = %d, want 3", len(res.Violations))
	}
	first := res.Violations[0]
	if first.Severity != "failure" || first.Message != "public bucket" || first.Namespace != "main" || first.Rule != "data.main.deny" {
		t.Fatalf("first violation = %+v", first)
	}
}

// Older conftest releases emitted successes as a list of rules; both shapes must
// count.
func TestParseConftestLegacySuccesses(t *testing.T) {
	res, err := parseConftest([]byte(`[{"namespace":"main","successes":["a","b"],"failures":[],"warnings":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Successes != 2 || res.FailureCount != 0 {
		t.Fatalf("res = %+v", res)
	}
}

// No output at all (conftest died before evaluating) must be an error, so an
// enforcing gate blocks instead of silently passing.
func TestParseConftestEmptyIsError(t *testing.T) {
	if _, err := parseConftest(nil); err == nil {
		t.Fatal("expected an error for empty conftest output")
	}
	if _, err := parseConftest([]byte("Error: rego_parse_error")); err == nil {
		t.Fatal("expected an error for non-JSON conftest output")
	}
}
