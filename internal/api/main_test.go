package api

import (
	"os"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/notify"
)

// TestMain relaxes the webhook destination policy for the whole package.
//
// notify refuses loopback destinations by default (that is where ArcLoom's own
// API and every other unguarded admin interface listens, and a webhook is
// configurable with only the `resources` permission). Every notification test
// here points at an httptest server, which can only bind 127.0.0.1 — so without
// this the tests would be exercising the refusal rather than the delivery they
// are about. The refusal itself is covered in internal/notify.
func TestMain(m *testing.M) {
	notify.AllowLoopback(true)
	os.Exit(m.Run())
}
