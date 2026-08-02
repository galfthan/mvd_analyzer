package analyzer

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// IdentityAnalyzer reconstructs player identity across reconnects.
//
// Every per-player ("Family A") output accumulates events keyed by wire
// slot and historically resolved that slot to its *final* occupant's
// name (Context.ResolveSlotDemoInfo). That breaks when a player
// disconnects and reconnects onto a different slot mid-match: the slot
// they vacated may be taken by someone else (or stamped with a late
// userinfo name), so their earlier events get relabelled with the wrong
// player. KTX itself unifies the player via its ghost mechanism
// (MakeGhost snapshots the departing player onto a ghost edict,
// ktx/src/client.c:2729-2799, and the next connection with the same
// netname restores it, :1464-1490 — the scoreboard row for that edict is
// published separately by ghost2scores, g_utils.c:2272-2356); this
// analyzer reproduces that unification for the pipeline.
//
// It does two things during the event pass:
//
//   - Tracks per-slot *sessions* — a session is one contiguous
//     occupancy of a wire slot by a single userid. A new session opens
//     when a slot's userid changes (a fresh connection); a plain name
//     change with the same userid stays one session (a rename, which
//     today's final-name resolution already handles correctly).
//   - Records the KTX reconnect broadcast prints
//     (`rejoins the game with`, `reenters the game without stats`),
//     which name the player that just reconnected.
//
// At PopulateCore it folds sessions into canonical identities and
// publishes a per-slot, time-sorted, identity-resolved session list on
// CoreOutputs. Downstream resolves an event by co.SlotIdentityAt(slot,
// tMs) instead of the slot's final name.
type IdentityAnalyzer struct {
	ctx *Context

	// occ is the shared wire-slot occupancy tracker (occupancy.go). One
	// occupancy == one session here.
	occ *occupancyTracker
	// reconnectPrefixes are the leading texts of the rejoin / reenter
	// broadcasts (events.PlayerRejoinEvent.Prefix — "<netname>" or
	// "<netname> [<team>]"); they are resolved to netnames in PopulateCore
	// once every session netname is known, since the userinfo precedes the
	// bprint but deferring keeps the prefix match robust against names that
	// contain the marker words.
	reconnectPrefixes []string
}

func NewIdentityAnalyzer() *IdentityAnalyzer {
	return &IdentityAnalyzer{occ: newOccupancyTracker()}
}

func (a *IdentityAnalyzer) Name() string { return "identity" }

func (a *IdentityAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

// Finalize is a no-op: the identity table is built in PopulateCore,
// which the registry runs after this analyser's Finalize and after the
// demoinfo analyser has set ctx.DemoInfo.
func (a *IdentityAnalyzer) Finalize(result *Result) error { return nil }

func (a *IdentityAnalyzer) OnEvent(event events.Event) error {
	switch e := event.(type) {
	case *events.UserInfoEvent:
		a.onUserInfo(e)
	case *events.PlayerRejoinEvent:
		a.reconnectPrefixes = append(a.reconnectPrefixes, e.Prefix)
	}
	return nil
}

// onUserInfo opens / continues / rotates the session for a slot via the
// shared occupancy tracker (occupancy.go), which splits an occupancy both
// on a userid change and on the server's drop broadcast
// (events.UserInfoEvent.Vacated) — so a player who times out stops owning
// the slot at the moment they left rather than at whatever lands on it
// next. The window between a drop and the next connection belongs to
// nobody, which is correct: an empty slot produces no events.
func (a *IdentityAnalyzer) onUserInfo(e *events.UserInfoEvent) {
	a.occ.onUserInfo(e) // boundaries are read from occ at PopulateCore
}

// PopulateCore folds sessions into canonical identities and writes the
// per-slot resolved session table onto CoreOutputs. Runs after the
// demoinfo analyser (identity declares a `requires` edge on `demoinfo`) so
// a.ctx.DemoInfo is available for the join.
func (a *IdentityAnalyzer) PopulateCore(co *CoreOutputs) {
	sess := a.occ.all()
	if len(sess) == 0 {
		return
	}

	idx := newDemoInfoIndex(a.ctx.DemoInfo)

	// Per-session demoinfo match (login → name). Distinct sessions that
	// resolve to the same demoinfo entry are the same human.
	demoMatch := make([]*DemoInfoPlayer, len(sess))
	for i, s := range sess {
		if dp, ok := idx.resolve(s.name, s.auth); ok {
			demoMatch[i] = dp
		}
	}

	// Which netnames KTX told us reconnected (primary signal). Match each
	// rejoin/reenter line against the known session netnames by prefix so
	// names containing spaces or marker words still resolve.
	reconnected := a.reconnectedNames()
	anyAuth := false
	for _, s := range sess {
		if s.auth != "" {
			anyAuth = true
			break
		}
	}

	uf := newUnionFind(len(sess))

	// Source 1 — same nonzero *auth login (authenticated identity).
	byAuth := make(map[string]int)
	for i, s := range sess {
		if s.auth == "" {
			continue
		}
		if j, ok := byAuth[s.auth]; ok {
			uf.union(i, j)
		} else {
			byAuth[s.auth] = i
		}
	}
	// Source 2 — same demoinfo player (login or name join).
	byDemo := make(map[*DemoInfoPlayer]int)
	for i, dp := range demoMatch {
		if dp == nil {
			continue
		}
		if j, ok := byDemo[dp]; ok {
			uf.union(i, j)
		} else {
			byDemo[dp] = i
		}
	}
	// Source 3 — KTX reconnect prints: every session whose netname KTX
	// announced as reconnecting is the same human.
	byReconName := make(map[string]int)
	for i, s := range sess {
		norm := normalizePlayerName(s.name)
		if !reconnected[norm] {
			continue
		}
		if j, ok := byReconName[norm]; ok {
			uf.union(i, j)
		} else {
			byReconName[norm] = i
		}
	}
	// Source 4 — fallback for bare demos (no demoinfo, no auth, no KTX
	// reconnect prints): unify by normalized netname. Restricted to that
	// case so we never over-merge two distinct same-name players on a
	// modern demo where the richer signals apply.
	if idx == nil && !anyAuth && len(reconnected) == 0 {
		byName := make(map[string]int)
		for i, s := range sess {
			norm := normalizePlayerName(s.name)
			if j, ok := byName[norm]; ok {
				uf.union(i, j)
			} else {
				byName[norm] = i
			}
		}
	}

	// Canonical display name + team per identity group. Prefer a
	// demoinfo match; else the latest session's netname/team.
	type ident struct {
		name, team string
		dp         *DemoInfoPlayer
		lastStart  int32
	}
	groups := make(map[int]*ident)
	for i, s := range sess {
		root := uf.find(i)
		g := groups[root]
		if g == nil {
			g = &ident{lastStart: math.MinInt32}
			groups[root] = g
		}
		if demoMatch[i] != nil && g.dp == nil {
			g.dp = demoMatch[i]
		}
		if s.startMs >= g.lastStart {
			g.lastStart = s.startMs
			if s.name != "" {
				g.name = s.name
				g.team = s.team
			}
		}
	}
	for _, g := range groups {
		if g.dp != nil {
			g.name = g.dp.Name
			if g.dp.Team != "" {
				g.team = g.dp.Team
			}
		}
	}

	// Build the per-slot, time-sorted resolved session list. The first
	// session on a slot extends back to -inf and the last forward to
	// +inf so events on the edges (before the first userinfo, after the
	// last) still resolve.
	sessions := make(map[int][]ResolvedSession)
	identityKey := func(root int) string { return "id:" + strconv.Itoa(root) }
	for i, s := range sess {
		root := uf.find(i)
		g := groups[root]
		sessions[s.slot] = append(sessions[s.slot], ResolvedSession{
			StartMs: s.startMs,
			EndMs:   s.endMs,
			Name:    g.name,
			Team:    g.team,
			Auth:    s.auth,
			// The occupancy's own userid, not the slot's: the tracker
			// already splits a session where the userid changes
			// (occupancy.go:196-198), so this is the connection that was
			// live for exactly this window.
			UserID:      s.userID,
			IdentityKey: identityKey(root),
		})
	}
	for slot := range sessions {
		ss := sessions[slot]
		sort.Slice(ss, func(i, j int) bool { return ss[i].StartMs < ss[j].StartMs })
		ss[0].StartMs = math.MinInt32
		ss[len(ss)-1].EndMs = math.MaxInt32
		sessions[slot] = ss
	}
	co.Sessions = sessions
}

// reconnectedNames resolves each stored rejoin/reenter prefix to the set
// of normalized netnames that reconnected. The prefix is "<name>" or
// "<name> [<team>]" with no delimiter between the two, and the netname can
// itself contain spaces, so we match against the known session netnames by
// longest prefix rather than trying to tokenize it.
func (a *IdentityAnalyzer) reconnectedNames() map[string]bool {
	out := make(map[string]bool)
	if len(a.reconnectPrefixes) == 0 {
		return out
	}
	// Distinct session netnames, longest first for prefix matching.
	recs := a.occ.all()
	names := make([]string, 0, len(recs))
	seen := make(map[string]bool)
	for _, s := range recs {
		if s.name != "" && !seen[s.name] {
			seen[s.name] = true
			names = append(names, s.name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	for _, prefix := range a.reconnectPrefixes {
		for _, n := range names {
			if prefix == n || strings.HasPrefix(prefix, n+" ") {
				out[normalizePlayerName(n)] = true
				break
			}
		}
	}
	return out
}

// --- union-find ---

type unionFind struct{ parent []int }

func newUnionFind(n int) *unionFind {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &unionFind{parent: p}
}

func (u *unionFind) find(i int) int {
	for u.parent[i] != i {
		u.parent[i] = u.parent[u.parent[i]]
		i = u.parent[i]
	}
	return i
}

func (u *unionFind) union(i, j int) {
	ri, rj := u.find(i), u.find(j)
	if ri != rj {
		u.parent[rj] = ri
	}
}
