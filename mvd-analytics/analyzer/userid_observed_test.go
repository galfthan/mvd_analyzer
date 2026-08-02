package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
	mvdsource "github.com/mvd-analyzer/mvd-reader/source/mvd"
)

// The invariant that should have caught the stale-userid defect: EVERY
// userid we report under a name must have been on the wire together with
// that name.
//
// Nothing asserted that before. `playerUserIDs` latched the first userid
// ever seen on a wire slot and kept it for the whole demo, so after a slot
// changed hands — or after the same player timed out and reconnected,
// drawing a fresh id — we published a userid belonging to a different
// connection, and every consumer that turned it into a hub `track=<id>`
// link watched the wrong player (gameId 220637: `(1)rusti (FU)` reported
// as 42, the id of a spectator who had left 10 minutes earlier). The
// roster/stream invariants in mvd-analytics/corpus could not see it: they
// compare names against names, and the name was right.
//
// The check is deliberately about the (name, userid) PAIR rather than
// about the userid alone, because every wrong value this class produces is
// a real userid from the same demo — just somebody else's.
//
// # Where it runs
//
// Over both demo drops available on a developer machine: the golden
// corpus's cached demos (testdata/cache, populated by TestGoldenCorpus,
// where all the known reconnect / handover cases live) and the
// per-machine special-cases directory that mvd-analytics/corpus walks. It
// lives here rather than in that package so it runs once per `make test`
// rather than twice (that package is deliberately re-run with -count=1),
// and so it can use normalizePlayerName for the join.
func TestReportedUserIDsWereObservedWithThatName(t *testing.T) {
	demos := userIDCorpusDemos()
	if len(demos) == 0 {
		t.Skip("no demos in testdata/cache or demo-test-data/mvd/special-cases — run TestGoldenCorpus once to populate the cache")
	}
	t.Logf("checking reported userids on %d demos", len(demos))
	for _, path := range demos {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := mvdsource.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer src.Close()
			// Tap the event stream the pipeline is reading rather than
			// replaying the demo a second time: parsing dominates the cost
			// of this walk, and the tap sees exactly the events the
			// analysers saw.
			tap := &userInfoTap{Source: src, byName: map[string]map[int]bool{}}
			res, err := NewDefaultRegistry().AnalyzeSource(tap, path)
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}
			checkReportedUserIDs(t, res, tap.byName)
		})
	}
}

// userIDCorpusDemos lists every demo available locally, from the golden
// cache first (that is where the reconnect cases are) and then the
// special-cases drop. Both are per-machine and may be absent.
func userIDCorpusDemos() []string {
	var out []string
	for _, pat := range []string{
		filepath.Join("..", "testdata", "cache", "*.mvd.gz"),
		filepath.Join("..", "..", "demo-test-data", "mvd", "special-cases", "*.mvd"),
		filepath.Join("..", "..", "demo-test-data", "mvd", "special-cases", "*.mvd.gz"),
	} {
		m, _ := filepath.Glob(pat)
		sort.Strings(m)
		out = append(out, m...)
	}
	return out
}

// userInfoTap is an events.Source pass-through that records which userids
// the wire carried under which netname, normalized for the join.
//
// Partial events are excluded: they are svc_setinfo syntheses whose UserID
// is the parser's cache, not a wire value (mvd-reader/parser/userinfo.go:63-86),
// so admitting them would let exactly the stale ids this invariant hunts
// for count as evidence. Vacated events are kept — a drop broadcast carries
// the departing client's own name and userid (sv_main.c:419-428).
type userInfoTap struct {
	events.Source
	byName map[string]map[int]bool
}

func (s *userInfoTap) Next() (events.Event, error) {
	ev, err := s.Source.Next()
	ui, ok := ev.(*events.UserInfoEvent)
	if !ok || ui.Partial || ui.Player == nil {
		return ev, err
	}
	name, uid := ui.Player.Name, ui.Player.UserID
	if name != "" && uid > 0 {
		norm := normalizePlayerName(name)
		if s.byName[norm] == nil {
			s.byName[norm] = map[int]bool{}
		}
		s.byName[norm][uid] = true
	}
	return ev, err
}

// checkReportedUserIDs asserts the (name, userid) pairs of every carrier
// the Result publishes: the playerUserIDs map itself and the four event
// streams that stamp a userid of their own.
func checkReportedUserIDs(t *testing.T, res *Result, observed map[string]map[int]bool) {
	t.Helper()
	ta := res.TimelineAnalysis
	if ta == nil {
		t.Skip("no timelineAnalysis on this demo")
	}
	checked := 0
	want := func(carrier, name string, uid int) {
		if uid <= 0 || name == "" {
			return // no id claimed; nothing to falsify
		}
		checked++
		norm := normalizePlayerName(name)
		if observed[norm][uid] {
			return
		}
		t.Errorf("%s reports userid %d for %q, but the wire never carried that id with that name (ids seen with it: %v)",
			carrier, uid, name, sortedIDs(observed[norm]))
	}

	names := make([]string, 0, len(ta.PlayerUserIDs))
	for name := range ta.PlayerUserIDs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want("playerUserIDs", name, ta.PlayerUserIDs[name])
	}
	for i, s := range ta.FragStreaks {
		want(fmt.Sprintf("fragStreaks[%d]", i), s.PlayerName, s.PlayerUserID)
	}
	for i, p := range ta.PowerupEvents {
		want(fmt.Sprintf("powerupEvents[%d]", i), p.PlayerName, p.PlayerUserID)
	}
	for i, m := range ta.DemoMarkers {
		want(fmt.Sprintf("demoMarkers[%d]", i), m.PlayerName, m.PlayerUserID)
	}
	for i, a := range ta.Airgibs {
		want(fmt.Sprintf("airgibs[%d].attacker", i), a.Attacker, a.AttackerUserID)
		want(fmt.Sprintf("airgibs[%d].victim", i), a.Victim, a.VictimUserID)
	}
	if checked == 0 {
		t.Skip("no userid reported on this demo — nothing to falsify")
	}
}

func sortedIDs(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// A demo file the cache does not have must not silently turn the walk into
// a no-op: os.ReadDir errors are swallowed by Glob, so assert the cache
// directory shape itself once.
func TestUserIDCorpusIsPopulated(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "testdata", "cache")); err != nil {
		t.Skip("no golden cache — run TestGoldenCorpus once")
	}
	if len(userIDCorpusDemos()) == 0 {
		t.Error("testdata/cache exists but no demo matched *.mvd.gz — the userid invariant would silently cover nothing")
	}
}
