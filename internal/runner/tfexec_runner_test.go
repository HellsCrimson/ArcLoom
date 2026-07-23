package runner

import (
	"errors"
	"testing"
)

// The init-retry heuristic recognizes the "lock no longer satisfies the
// configured constraint" failure (the one fixed by -upgrade) and nothing else.
func TestIsLockConstraintMismatch(t *testing.T) {
	mismatch := []string{
		`init: exit status 1 Error: Failed to resolve provider packages Could not resolve provider hashicorp/random: locked provider registry.opentofu.org/hashicorp/random 3.5.0 does not match configured version constraint 3.6.0; must use tofu init -upgrade to allow selection of new versions`,
		`Error: provider requirements cannot be satisfied by locked dependencies`,
	}
	for _, s := range mismatch {
		if !isLockConstraintMismatch(errors.New(s)) {
			t.Errorf("expected mismatch for %q", s)
		}
	}
	other := []string{
		"init: exit status 1 Error: Failed to install provider",
		"init: context deadline exceeded",
		"apply: exit status 1",
	}
	for _, s := range other {
		if isLockConstraintMismatch(errors.New(s)) {
			t.Errorf("did not expect mismatch for %q", s)
		}
	}
}
