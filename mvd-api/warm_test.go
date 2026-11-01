package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
	"github.com/mvd-analyzer/mvd-api/internal/democache"
)

// warmCatalog answers searches from a fixed list of rows, newest first, in
// pages, and records what it was asked.
type warmCatalog struct {
	rows  []map[string]any
	asked []hubfetch.SearchParams
}

func (c *warmCatalog) Search(_ context.Context, p hubfetch.SearchParams) (any, error) {
	c.asked = append(c.asked, p)
	var page []any
	for i := p.Offset; i < len(c.rows) && i < p.Offset+p.Limit; i++ {
		page = append(page, c.rows[i])
	}
	return map[string]any{"games": page}, nil
}

func (c *warmCatalog) Game(context.Context, int) (map[string]any, error) { return nil, nil }

// warmFake records Warm calls; ready games report nothing done, failing
// ones return their error, and each call advances the clock by cost.
type warmFake struct {
	calls []int
	ready map[int]bool
	fail  map[int]error
	clock *time.Time
	cost  time.Duration
}

func (s *warmFake) Warm(_ context.Context, g hubfetch.GameInfo) (bool, error) {
	s.calls = append(s.calls, g.ID)
	*s.clock = s.clock.Add(s.cost)
	if err := s.fail[g.ID]; err != nil {
		return true, err
	}
	return !s.ready[g.ID], nil
}

func warmRow(id int, at time.Time) map[string]any {
	return map[string]any{"id": float64(id), "timestamp": at.Format(time.RFC3339), "demo_sha256": fmt.Sprintf("%064x", id)}
}

func newTestWarmer(cat *warmCatalog, store *warmFake, now *time.Time) *warmer {
	w := newWarmer(cat, store, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Minute, 7*24*time.Hour)
	w.now = func() time.Time { return *now }
	store.clock = now
	return w
}

// A tick prepares the window's games newest first, skips ones without a
// demo or older than the window, and leaves alone what is already done.
func TestWarmer_TickOrderWindowAndDone(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cat := &warmCatalog{rows: []map[string]any{
		warmRow(5, now.Add(-time.Hour)),
		{"id": float64(4), "timestamp": now.Add(-2 * time.Hour).Format(time.RFC3339), "demo_sha256": ""},
		warmRow(3, now.Add(-24*time.Hour)),
		warmRow(2, now.Add(-8*24*time.Hour)),
	}}
	store := &warmFake{ready: map[int]bool{3: true}}
	w := newTestWarmer(cat, store, &now)

	w.tick(context.Background())
	if fmt.Sprint(store.calls) != "[5 3]" {
		t.Fatalf("warmed %v, want [5 3] (newest first; 4 has no demo, 2 is past the window)", store.calls)
	}
	if cat.asked[0].From != "2026-10-25" || cat.asked[0].Limit != hubfetch.MaxSearchLimit {
		t.Errorf("asked %+v", cat.asked[0])
	}

	cat.rows = append([]map[string]any{warmRow(6, now)}, cat.rows...)
	store.calls = nil
	w.tick(context.Background())
	if fmt.Sprint(store.calls) != "[6]" {
		t.Fatalf("second tick warmed %v, want only the new game", store.calls)
	}
}

// A tick that outlasts the interval stops; the next one lists again, so a
// game published meanwhile goes before the rest of the backfill.
func TestWarmer_LongTickYieldsToNewGames(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var rows []map[string]any
	for id := 100; id > 90; id-- {
		rows = append(rows, warmRow(id, now.Add(-time.Duration(101-id)*time.Hour)))
	}
	cat := &warmCatalog{rows: rows}
	store := &warmFake{cost: 2 * time.Minute}
	w := newTestWarmer(cat, store, &now)

	w.tick(context.Background())
	if fmt.Sprint(store.calls) != "[100 99 98]" {
		t.Fatalf("first tick warmed %v, want three (2 min each, 5 min interval)", store.calls)
	}
	cat.rows = append([]map[string]any{warmRow(200, now)}, cat.rows...)
	store.calls = nil
	w.tick(context.Background())
	if store.calls[0] != 200 || store.calls[1] != 97 {
		t.Fatalf("second tick warmed %v, want the new game first, then the backfill", store.calls)
	}
}

// An upstream failure is retried for a few ticks; a demo that does not
// parse is not.
func TestWarmer_Failures(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cat := &warmCatalog{rows: []map[string]any{warmRow(2, now), warmRow(1, now)}}
	store := &warmFake{fail: map[int]error{
		2: fmt.Errorf("%w: cdn 503", democache.ErrHubUpstream),
		1: fmt.Errorf("%w: bad demo", democache.ErrParse),
	}}
	w := newTestWarmer(cat, store, &now)
	for i := 0; i < warmTries+2; i++ {
		w.tick(context.Background())
	}
	n := map[int]int{}
	for _, id := range store.calls {
		n[id]++
	}
	if n[2] != warmTries || n[1] != 1 {
		t.Fatalf("tries %v, want game 2 %d times (upstream), game 1 once (parse)", n, warmTries)
	}
}
