package api

import "testing"

// TestRedactorReplacesInjectedSecrets: the run log is persisted and readable by
// anyone with project read, and providers quote the request they failed on.
func TestRedactorReplacesInjectedSecrets(t *testing.T) {
	r := newRedactor(
		map[string]string{"db_password": "sup3rs3cr3tvalue", "short": "ab"},
		map[string]string{"VAULT_TOKEN": "hvs.CAESIJlongtokenvalue"},
	)
	got := r.line("Error: auth failed using hvs.CAESIJlongtokenvalue for user with password sup3rs3cr3tvalue")
	want := "Error: auth failed using (secret) for user with password (secret)"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	// A value below the length floor is left alone: replacing "ab" would hit
	// every ordinary word and destroy the log's diagnostic value.
	if out := r.line("variable ab is unset"); out != "variable ab is unset" {
		t.Errorf("short value should not be redacted: %q", out)
	}
	// Nothing to redact must be a pass-through.
	if out := r.line("Plan: 1 to add"); out != "Plan: 1 to add" {
		t.Errorf("unexpected rewrite: %q", out)
	}
}

// A secret that contains another as a prefix must be replaced whole, or the
// longer one leaves a readable tail behind.
func TestRedactorPrefersLongestMatch(t *testing.T) {
	r := newRedactor(map[string]string{"a": "tokenvalue", "b": "tokenvalue-extended"})
	if got := r.line("using tokenvalue-extended here"); got != "using (secret) here" {
		t.Errorf("got %q", got)
	}
}

func TestRedactorNilSafe(t *testing.T) {
	var r *redactor
	if got := r.line("x"); got != "x" {
		t.Errorf("nil redactor must pass through, got %q", got)
	}
	if got := newRedactor(nil).line("x"); got != "x" {
		t.Errorf("empty redactor must pass through, got %q", got)
	}
}
