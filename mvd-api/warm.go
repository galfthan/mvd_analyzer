package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
	"github.com/mvd-analyzer/mvd-api/internal/democache"
)

// The hub warmer (-warm-interval, -warm-window) prepares hub demos before
// anyone opens one: it lists the window's games from the hub catalog,
// newest first, and has the cache prepare each one it has not prepared yet
// (democache.Cache.Warm: the demo, its parsed Result and its position
// pack), so the web page's first load of a new demo is a cache read. The
// first tick is the window's backfill; later ones, an interval apart, find
// the few games published since. A tick that runs out of its interval
// stops and the next one starts at once, listing again, so the backfill
// runs through while games published meanwhile go first. One demo at a
// time, through the cache's parse semaphore like a visitor's request, so
// warming takes at most one of its slots.
//
// Warmed files are written now, so they are the newest in the cache: the
// tier GC (oldest mtime first, past -cache-max-bytes) evicts them only once
// they are older than everything else, opened demos having their files
// touched on every read. Size -cache-max-bytes for the window on top of
// what visitors keep, or the backfill evicts their history (and then,
// newest first, its own).

// warmStore is what the warmer needs of the demo cache.
type warmStore interface {
	Warm(ctx context.Context, game hubfetch.GameInfo) (bool, error)
}

type warmer struct {
	catalog  gameCatalog
	store    warmStore
	logger   *slog.Logger
	interval time.Duration
	window   time.Duration
	now      func() time.Time

	// done holds the games prepared, found prepared or given up on, with
	// their timestamps, so a tick skips them without asking the cache;
	// entries leave once they fall out of the window. A game that failed to
	// download or be written waits until retryAt, its wait doubling with
	// each of its fails.
	done    map[int]time.Time
	fails   map[int]int
	retryAt map[int]time.Time
}

const (
	// warmMaxBackoff caps a failing game's wait. A demo whose CDN copy is
	// broken is fetched about this often while it is in the window; one
	// that failed in an outage is warmed at most this long after it.
	warmMaxBackoff = time.Hour
	// warmOutage is how many failures in a row end a tick: the hub, its CDN
	// or the disk is down, and the rest of the list would only fail too.
	warmOutage = 5
)

func newWarmer(catalog gameCatalog, store warmStore, logger *slog.Logger, interval, window time.Duration) *warmer {
	return &warmer{catalog: catalog, store: store, logger: logger, interval: interval, window: window,
		now: time.Now, done: map[int]time.Time{}, fails: map[int]int{}, retryAt: map[int]time.Time{}}
}

// run ticks until ctx ends: at once, then an interval after each tick that
// finished its list, or at once again after one that ran out of time.
func (w *warmer) run(ctx context.Context) {
	for ctx.Err() == nil {
		if w.tick(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.interval):
		}
	}
}

// warmGame is one listed game: its download info and when it was played.
type warmGame struct {
	info hubfetch.GameInfo
	at   time.Time
}

// warmPermanent reports a failure that is the demo's own — it does not
// parse, or the hub has no such game — which a visitor would meet too, so
// the game is not tried again. Any other (a download, a full disk) is
// retried with a backoff.
func warmPermanent(err error) bool {
	return errors.Is(err, democache.ErrParse) || errors.Is(err, democache.ErrDemoNotFound) ||
		errors.Is(err, democache.ErrInvalidDemoID)
}

// tick works through one listing; it reports whether it ran out of its
// interval with games left.
func (w *warmer) tick(ctx context.Context) bool {
	start := w.now()
	since := start.Add(-w.window)
	games, err := w.list(ctx, since)
	if err != nil {
		w.logger.Warn("hub warm: listing failed", "err", err.Error())
		return false
	}
	var warmed, failed, left, inARow int
	outOfTime := false
	listed := make(map[int]bool, len(games))
	for _, g := range games {
		listed[g.info.ID] = true
		if _, ok := w.done[g.info.ID]; ok {
			continue
		}
		if at, ok := w.retryAt[g.info.ID]; ok && w.now().Before(at) {
			continue
		}
		if inARow >= warmOutage {
			left++
			continue
		}
		if w.now().Sub(start) >= w.interval {
			outOfTime = true
			left++
			continue
		}
		did, err := w.store.Warm(ctx, g.info)
		if ctx.Err() != nil {
			return false
		}
		switch {
		case err == nil:
			inARow = 0
			if did {
				warmed++
			}
			w.done[g.info.ID] = g.at
		case warmPermanent(err):
			failed++
			w.logger.Warn("hub warm: game failed", "gameId", g.info.ID, "err", err.Error())
			w.done[g.info.ID] = g.at
		default:
			failed++
			inARow++
			w.fails[g.info.ID]++
			wait := min(w.interval<<(w.fails[g.info.ID]-1), warmMaxBackoff)
			w.retryAt[g.info.ID] = w.now().Add(wait)
			w.logger.Warn("hub warm: game failed", "gameId", g.info.ID, "retryIn", wait.String(), "err", err.Error())
		}
	}
	for id, at := range w.done {
		if at.Before(since) {
			delete(w.done, id)
		}
	}
	for id := range w.fails {
		if _, isDone := w.done[id]; isDone || !listed[id] {
			delete(w.fails, id)
			delete(w.retryAt, id)
		}
	}
	if warmed > 0 || failed > 0 || left > 0 {
		w.logger.Info("hub warm", "listed", len(games), "warmed", warmed, "failed", failed,
			"left", left, "outage", inARow >= warmOutage, "took", w.now().Sub(start).Round(time.Second).String())
	}
	return outOfTime
}

// warmMaxPages bounds one tick's listing: 20 pages of 1000 games, far past
// any week of hub traffic (~1300 games).
const warmMaxPages = 20

// list returns the window's games with a demo, newest first (the catalog's
// order). The catalog filters by day; the window's exact start is applied
// here. A game whose timestamp does not parse is kept.
func (w *warmer) list(ctx context.Context, since time.Time) ([]warmGame, error) {
	var games []warmGame
	seen := map[int]bool{}
	from := since.UTC().Format("2006-01-02")
	for page := 0; page < warmMaxPages; page++ {
		ctx, cancel := context.WithTimeout(ctx, gamesSearchTimeout)
		out, err := w.catalog.Search(ctx, hubfetch.SearchParams{From: from, Limit: hubfetch.MaxSearchLimit, Offset: page * hubfetch.MaxSearchLimit})
		cancel()
		if err != nil {
			return nil, err
		}
		body, _ := out.(map[string]any)
		rows, _ := body["games"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			id, _ := row["id"].(float64)
			sha, _ := row["demo_sha256"].(string)
			// Games published while paging shift the offsets: a row may come
			// again on the next page.
			if id <= 0 || sha == "" || seen[int(id)] {
				continue
			}
			seen[int(id)] = true
			at := w.now()
			if ts, ok := row["timestamp"].(string); ok {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					if t.Before(since) {
						continue
					}
					at = t
				}
			}
			src, _ := row["demo_source_url"].(string)
			games = append(games, warmGame{info: hubfetch.GameInfo{ID: int(id), DemoSHA256: sha, DemoSourceURL: src}, at: at})
		}
		if len(rows) < hubfetch.MaxSearchLimit {
			break
		}
	}
	return games, nil
}
