package main

import (
	"math"
	"sync"
	"time"

	"github.com/mvd-analyzer/mvd-api/internal/authkeys"
)

// tokenBucket is a lazy (no background goroutine) token bucket. Tokens refill
// continuously at `rate` per second up to `burst`; each admitted request costs
// one token. It is kept in mvd-api rather than pulling in golang.org/x/time/rate
// so the module stays dependency-free (its only requires are the sibling
// workspace modules) — the algorithm is ~40 lines and needs no eviction at the
// tens-of-keys scale this store operates at (PLAN-hosting D8).
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64 // bucket capacity
	tokens float64
	last   time.Time
}

func newTokenBucket(ratePerSec float64, burst int) *tokenBucket {
	return &tokenBucket{
		rate:   ratePerSec,
		burst:  float64(burst),
		tokens: float64(burst), // start full so a fresh key isn't immediately throttled
		last:   time.Now(),
	}
}

// allow refills for elapsed time and, if a whole token is available, spends one
// and returns (true, 0). Otherwise it returns (false, retryAfter) where
// retryAfter is how long until one token accrues. now is injected so tests are
// deterministic; production callers pass time.Now().
func (b *tokenBucket) allow(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(b.burst, b.tokens+elapsed*b.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	if b.rate <= 0 {
		// A zero/negative rate never refills; report a nominal 1s backoff.
		return false, time.Second
	}
	need := 1 - b.tokens
	return false, time.Duration(need / b.rate * float64(time.Second))
}

// rateClass bundles the per-key limits for one traffic class.
type rateClass struct {
	rate  float64
	burst int
}

// keyLimiter holds one token bucket per key hash, in the user or service
// class — and, for a per-client key (the web page's, which its proxy adds
// for every visitor), one per key and client IP in the web class. Buckets
// are created lazily on first sight of an *authenticated* key, so unknown
// keys (rejected with 401 before the limiter runs) never allocate. Per-key
// buckets are tens and are never evicted; per-client buckets grow with the
// visitors, so once there are maxClientBuckets of them, those idle long
// enough to have refilled — indistinguishable from a fresh bucket — are
// dropped.
type keyLimiter struct {
	user    rateClass
	service rateClass
	web     rateClass

	mu            sync.Mutex
	buckets       map[string]*tokenBucket
	clientBuckets map[string]*tokenBucket
	nowFn         func() time.Time // injectable for tests
}

// maxClientBuckets is how many per-client buckets the limiter holds before
// it sweeps out the idle ones.
const maxClientBuckets = 4096

func newKeyLimiter(user, service, web rateClass) *keyLimiter {
	return &keyLimiter{
		user:          user,
		service:       service,
		web:           web,
		buckets:       make(map[string]*tokenBucket),
		clientBuckets: make(map[string]*tokenBucket),
		nowFn:         time.Now,
	}
}

// allow admits one request for rec (from client, the caller's IP, which
// only a per-client key reads), returning the Retry-After hint when it
// does not.
func (l *keyLimiter) allow(rec authkeys.Record, client string) (bool, time.Duration) {
	now := l.nowFn()
	l.mu.Lock()
	var b *tokenBucket
	if rec.PerClient {
		id := rec.KeyHash + " " + client
		if b = l.clientBuckets[id]; b == nil {
			if len(l.clientBuckets) >= maxClientBuckets {
				l.sweepIdleLocked(now)
			}
			b = newTokenBucket(l.web.rate, l.web.burst)
			l.clientBuckets[id] = b
		}
	} else if b = l.buckets[rec.KeyHash]; b == nil {
		class := l.user
		if rec.Service {
			class = l.service
		}
		b = newTokenBucket(class.rate, class.burst)
		l.buckets[rec.KeyHash] = b
	}
	l.mu.Unlock()
	return b.allow(now)
}

// sweepIdleLocked drops the per-client buckets that have refilled to full
// since their last request: a new bucket starts full, so dropping one
// changes nothing its client would see. l.mu must be held.
func (l *keyLimiter) sweepIdleLocked(now time.Time) {
	if l.web.rate <= 0 {
		return
	}
	full := time.Duration(float64(l.web.burst) / l.web.rate * float64(time.Second))
	for id, b := range l.clientBuckets {
		b.mu.Lock()
		idle := now.Sub(b.last)
		b.mu.Unlock()
		if idle >= full {
			delete(l.clientBuckets, id)
		}
	}
}

// numBuckets reports how many per-key buckets exist. Used by tests to pin the
// DoS-guard invariant that unknown (401'd) keys never allocate a bucket — the
// limiter map must not be growable by unauthenticated traffic.
func (l *keyLimiter) numBuckets() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets) + len(l.clientBuckets)
}
