package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Login / password throttle defaults. Each password endpoint runs an argon2id
// hash (64 MiB, t=3), so an unthrottled burst is both a credential brute-force
// channel and an unauthenticated memory-DoS. A token bucket caps that: a short
// human burst is allowed, then one attempt trickles back every refill period.
// A *successful* login refunds the key, so real users never lock themselves out
// — only failing attempts deplete.
const (
	loginBurst  = 10              // immediate attempts before throttling
	loginRefill = 3 * time.Second // one attempt is restored this often
)

// rateLimiter is a per-key token-bucket limiter. Buckets refill continuously;
// idle buckets that have fully refilled carry no state and are reaped so a
// spray of distinct keys can't grow the map without bound.
type rateLimiter struct {
	burst  float64
	refill float64 // tokens per second
	max    int     // hard cap on tracked keys
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter builds a limiter allowing an initial burst then one token per
// refillEvery.
func newRateLimiter(burst int, refillEvery time.Duration) *rateLimiter {
	return &rateLimiter{
		burst:   float64(burst),
		refill:  1 / refillEvery.Seconds(),
		max:     16384,
		now:     time.Now,
		buckets: map[string]*rateBucket{},
	}
}

// allow consumes one token for key. It returns whether the request may proceed
// and, if not, how long until a token frees up.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.max {
			l.evictLocked(now)
		}
		b = &rateBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		b.tokens += now.Sub(b.last).Seconds() * l.refill
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.refill * float64(time.Second))
	return false, wait
}

// reset drops a key's bucket, refunding it to full (used after a successful
// auth so legitimate use never accumulates toward a lockout).
func (l *rateLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// evictLocked reaps buckets that have fully refilled (no rate-limiting state
// worth keeping); if that frees nothing, it drops the oldest. Caller holds mu.
func (l *rateLimiter) evictLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.refill >= l.burst {
			delete(l.buckets, k)
			continue
		}
		if oldestKey == "" || b.last.Before(oldest) {
			oldestKey, oldest = k, b.last
		}
	}
	if len(l.buckets) >= l.max && oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

// clientIP returns the direct peer address (host without port). It deliberately
// ignores X-Forwarded-For, which a client can spoof to dodge the per-IP limit;
// behind a reverse proxy the per-IP bucket is therefore per-proxy, and the
// per-username/per-user buckets carry the brute-force defense.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tooManyAttempts writes a 429 with a Retry-After hint and a generic message
// (never revealing whether a username exists).
func tooManyAttempts(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeErr(w, http.StatusTooManyRequests, "too many attempts, please try again later")
}
