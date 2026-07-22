package api

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimiter_BurstThenThrottleThenRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(3, 10*time.Second)
	l.now = func() time.Time { return now }

	// The burst is allowed.
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("k"); !ok {
			t.Fatalf("attempt %d should be allowed within the burst", i+1)
		}
	}
	// The next is throttled, with a positive retry hint.
	ok, retry := l.allow("k")
	if ok {
		t.Fatal("attempt past the burst should be throttled")
	}
	if retry <= 0 || retry > 10*time.Second {
		t.Fatalf("retry hint %v out of range", retry)
	}
	// After one refill period a single attempt frees up.
	now = now.Add(10 * time.Second)
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("one token should have refilled")
	}
	if ok, _ := l.allow("k"); ok {
		t.Fatal("only one token should have refilled")
	}
	// A different key has its own independent bucket.
	if ok, _ := l.allow("other"); !ok {
		t.Fatal("independent key should start with a full bucket")
	}
}

func TestRateLimiter_ResetRefunds(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	l.allow("k")
	l.allow("k")
	if ok, _ := l.allow("k"); ok {
		t.Fatal("bucket should be empty")
	}
	l.reset("k")
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("reset should refund the bucket to full")
	}
}

// A spray of distinct keys must not grow the map past the hard cap: refilled
// buckets are reaped and, failing that, the oldest is evicted.
func TestRateLimiter_BoundedKeys(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(1, time.Second)
	l.max = 100
	l.now = func() time.Time { return now }

	for i := 0; i < 5000; i++ {
		now = now.Add(time.Millisecond)
		l.allow(string(rune(i%1000)) + "-" + time.Duration(i).String())
	}
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n > l.max {
		t.Fatalf("tracked keys %d exceed the cap %d", n, l.max)
	}
}

// A flood of failed logins from one IP is throttled to 429 (with Retry-After),
// while the auth response stays generic for the attempts that do run.
func TestLogin_FloodThrottled(t *testing.T) {
	h, _, _ := newRBACServer(t)

	body := `{"username":"viewer","password":"wrong"}`
	var got429 bool
	for i := 0; i < loginBurst+3; i++ {
		rec := do(t, h, http.MethodPost, "/api/auth/login", nil, body)
		switch {
		case rec.Code == http.StatusUnauthorized:
			// expected for attempts within the burst
		case rec.Code == http.StatusTooManyRequests:
			got429 = true
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 response must carry a Retry-After header")
			}
		default:
			t.Fatalf("attempt %d: unexpected status %d", i+1, rec.Code)
		}
	}
	if !got429 {
		t.Fatalf("a flood of %d attempts should eventually be throttled", loginBurst+3)
	}
}

// Successful logins refund their buckets, so a legitimate user is never locked
// out by repeated (successful) logins.
func TestLogin_SuccessNeverLocksOut(t *testing.T) {
	h, _, _ := newRBACServer(t)
	for i := 0; i < loginBurst*2; i++ {
		if c := login(t, h, "viewer", "password1"); c == nil {
			t.Fatalf("login %d failed", i+1)
		}
	}
}
