package main

import (
	"sync"
	"time"
)

// uploadLedger is a per-key daily upload budget: it tracks how many demo
// bytes and how many demos each API key has uploaded on the current UTC day,
// and rejects a charge that would exceed either dimension. It is an
// abuse-damping guard, not a billing ledger — it lives in memory, keyed by the
// key hash, and resets on restart (a restart is a rare, operator-initiated
// event; the point is to blunt a runaway loop, not to bill). At the tens-of-
// keys scale the auth store operates at, the map needs no eviction: a stale
// per-key entry is a couple of ints, and it is overwritten the next day.
//
// Only authenticated requests reach here (the handler skips the quota entirely
// in no-auth mode, where there is no key identity), so the map cannot be grown
// by unauthenticated traffic.
type uploadLedger struct {
	mu    sync.Mutex
	days  map[string]*uploadDay
	nowFn func() time.Time // injectable for tests
}

// uploadDay is one key's usage for a single UTC day.
type uploadDay struct {
	day   string // UTC date, "2006-01-02"
	bytes int64
	count int64
}

func newUploadLedger() *uploadLedger {
	return &uploadLedger{days: map[string]*uploadDay{}, nowFn: time.Now}
}

// charge admits an upload of size bytes for keyHash, checking it against the
// per-key daily limits (a <= 0 limit disables that dimension). On success it
// records the bytes + one demo and returns (true, 0). On rejection it records
// nothing and returns (false, retryAfter), where retryAfter is the time until
// the next UTC day boundary (when the budget resets).
func (l *uploadLedger) charge(keyHash string, size, dailyBytes, dailyCount int64) (bool, time.Duration) {
	now := l.nowFn().UTC()
	day := now.Format("2006-01-02")

	l.mu.Lock()
	defer l.mu.Unlock()

	e := l.days[keyHash]
	if e == nil || e.day != day {
		e = &uploadDay{day: day}
		l.days[keyHash] = e
	}

	if dailyCount > 0 && e.count+1 > dailyCount {
		return false, untilNextUTCDay(now)
	}
	if dailyBytes > 0 && e.bytes+size > dailyBytes {
		return false, untilNextUTCDay(now)
	}

	e.count++
	e.bytes += size
	return true, 0
}

// untilNextUTCDay is the duration from now to the next UTC midnight — when the
// daily budget rolls over — used as the 429 Retry-After hint.
func untilNextUTCDay(now time.Time) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	return next.Sub(now)
}
