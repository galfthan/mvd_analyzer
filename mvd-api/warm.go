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
// anyone opens one: every interval it lists the window's games from the hub
// catalog, newest first, and has the cache prepare each one it has not
// prepared yet (democache.Cache.Warm: the demo, its parsed Result and its
// position pack), so the web page's first load of a new demo is a cache
// read. The first tick is the window's backfill; later ones find the few
// games published since. A tick that outlasts the interval stops and the
// next one lists again, so new games go ahead of what is left of a
// backfill. One demo at a time, through the cache's parse semaphore like a
// visitor's request, so warming takes at most one of its slots. The tier
// GC evicts the oldest files past -cache-max-bytes as ever; a warmed demo
// nobody opens is never touched, so it goes first.

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
	// entries leave once they fall out of the window. tries counts a game's
	// upstream failures.
	done  map[int]time.Time
	tries map[int]int
}

// warmTries is how many ticks a game whose download fails is tried in: the
// hub or its CDN may be down for a while, but a demo whose CDN copy is
// broken would otherwise be fetched again every interval.
const warmTries = 3

func newWarmer(catalog gameCatalog, store warmStore, logger *slog.Logger, interval, window time.Duration) *warmer {
	return &warmer{catalog: catalog, store: store, logger: logger, interval: interval, window: window,
		now: time.Now, done: map[int]time.Time{}, tries: map[int]int{}}
}

// run ticks until ctx ends: at once, then an interval after each tick.
func (w *warmer) run(ctx context.Context) {
	for {
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.interval):
		}
	}
}

// warmGame is one listed game: its download info and when it was played.
type warmGame struct {
	info hubfetch.GameInfo
	at   time.Time
}

func (w *warmer) tick(ctx context.Context) {
	start := w.now()
	since := start.Add(-w.window)
	games, err := w.list(ctx, since)
	if err != nil {
		w.logger.Warn("hub warm: listing failed", "err", err.Error())
		return
	}
	var warmed, failed, left int
	for _, g := range games {
		if _, ok := w.done[g.info.ID]; ok {
			continue
		}
		if w.now().Sub(start) >= w.interval {
			left++
			continue
		}
		did, err := w.store.Warm(ctx, g.info)
		if ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, democache.ErrHubUpstream):
			// The hub or its CDN failing: try again next tick, a few times.
			failed++
			w.tries[g.info.ID]++
			w.logger.Warn("hub warm: game failed", "gameId", g.info.ID, "try", w.tries[g.info.ID], "err", err.Error())
			if w.tries[g.info.ID] < warmTries {
				continue
			}
		case err != nil:
			// The demo itself (it does not parse): a visitor would get the
			// same error, so it is not retried.
			failed++
			w.logger.Warn("hub warm: game failed", "gameId", g.info.ID, "err", err.Error())
		case did:
			warmed++
		}
		w.done[g.info.ID] = g.at
	}
	for id, at := range w.done {
		if at.Before(since) {
			delete(w.done, id)
			delete(w.tries, id)
		}
	}
	if warmed > 0 || failed > 0 || left > 0 {
		w.logger.Info("hub warm", "listed", len(games), "warmed", warmed, "failed", failed,
			"left", left, "took", w.now().Sub(start).Round(time.Second).String())
	}
}

// warmMaxPages bounds one tick's listing: 20 pages of 1000 games, far past
// any week of hub traffic (~1300 games).
const warmMaxPages = 20

// list returns the window's games with a demo, newest first (the catalog's
// order). The catalog filters by day; the window's exact start is applied
// here. A game whose timestamp does not parse is kept.
func (w *warmer) list(ctx context.Context, since time.Time) ([]warmGame, error) {
	var games []warmGame
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
			if id <= 0 || sha == "" {
				continue
			}
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
