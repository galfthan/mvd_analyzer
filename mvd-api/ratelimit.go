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
// for every visitor), one per key and client in the web class. Buckets are
// created lazily on first sight of an *authenticated* key, so unknown keys
// (rejected with 401 before the limiter runs) never allocate. Per-key
// buckets are tens and are never evicted. Per-client buckets grow with the
// visitors, so they are swept: whenever their number has doubled since the
// last sweep, the ones idle long enough to have refilled — no different
// from a fresh bucket — are dropped, which keeps the sweep's cost
// amortised over the inserts that grew the map. A burst of new clients
// faster than buckets refill is held under maxClientBuckets: reaching it
// drops arbitrary buckets down to half of it (amortised again), which only
// hands those clients a fresh burst.
type keyLimiter struct {
	user    rateClass
	service rateClass
	web     rateClass

	mu            sync.Mutex
	buckets       map[string]*tokenBucket
	clientBuckets map[string]*tokenBucket
	nextSweep     int              // sweep when clientBuckets reaches this
	nowFn         func() time.Time // injectable for tests
}

const (
	// firstClientSweep is the per-client bucket count of the first sweep.
	firstClientSweep = 4096
	// maxClientBuckets caps the per-client buckets (a few MB).
	maxClientBuckets = 65536
)

func newKeyLimiter(user, service, web rateClass) *keyLimiter {
	return &keyLimiter{
		user:          user,
		service:       service,
		web:           web,
		buckets:       make(map[string]*tokenBucket),
		clientBuckets: make(map[string]*tokenBucket),
		nextSweep:     firstClientSweep,
		nowFn:         time.Now,
	}
}

// allow admits one request for rec (from client, the caller's address as
// limiterClient gives it, which only a per-client key reads), returning
// the Retry-After hint when it does not.
func (l *keyLimiter) allow(rec authkeys.Record, client string) (bool, time.Duration) {
	now := l.nowFn()
	l.mu.Lock()
	var b *tokenBucket
	if rec.PerClient {
		id := rec.KeyHash + " " + client
		if b = l.clientBuckets[id]; b == nil {
			if len(l.clientBuckets) >= l.nextSweep {
				l.sweepLocked(now)
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

// sweepLocked drops the per-client buckets that have refilled to full since
// their last request — then, if maxClientBuckets are still held, arbitrary
// ones down to half of it — and sets the next sweep at twice what is left,
// never past the cap. l.mu must be held.
func (l *keyLimiter) sweepLocked(now time.Time) {
	if l.web.rate > 0 {
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
	if len(l.clientBuckets) >= maxClientBuckets {
		for id := range l.clientBuckets {
			if len(l.clientBuckets) <= maxClientBuckets/2 {
				break
			}
			delete(l.clientBuckets, id)
		}
	}
	l.nextSweep = min(max(firstClientSweep, 2*len(l.clientBuckets)), maxClientBuckets)
}

// numBuckets reports how many per-key buckets exist. Used by tests to pin the
// DoS-guard invariant that unknown (401'd) keys never allocate a bucket — the
// limiter map must not be growable by unauthenticated traffic.
func (l *keyLimiter) numBuckets() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets) + len(l.clientBuckets)
}
