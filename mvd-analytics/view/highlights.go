package view

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Highlight detection tuning.
const (
	// highlightSnapshotLeadMs is how far BEFORE the event instant the value
	// streams (health, armor, cells, active weapon) are read. The obituary
	// and the death-frame stat broadcast share one MVD frame — measured on
	// gameId 212260 every telefrag victim carries a -99 health sample AT
	// the frag time — so the sample at t is already the corpse; one
	// millisecond earlier is the last pre-hit value, and it reproduces
	// KTX's own armor+health telefrag figure (Damage.Telefrags[].bounded)
	// exactly on that corpus.
	highlightSnapshotLeadMs = 1

	// highlightIntervalTolMs is the look-back for the interval streams
	// (weapons, powerups): a field counts as held when an interval overlaps
	// [t - tol, t]. KTX strips the quad on the death frame, so a quad that
	// ENDS at t must still count; 100 ms is tight on purpose — a rocket
	// landing after the quad expired deals normal damage, and is no
	// quadbore.
	highlightIntervalTolMs = 100

	// dischargeClusterMs groups the evidence of one discharge — the
	// discharger's own death print, the per-victim kill prints, and the
	// damage-log radius hits — into one event. In truth they share a
	// server frame (T_RadiusDamage runs in one think, ktx/src/weapons.c:1208),
	// but the print and the damage stamp can straddle an MVD frame.
	dischargeClusterMs = 500

	// quadboreSelfHitBackMs bounds the search for the fatal self-hit in
	// the damage log, backwards from the obituary: the rocket's splash
	// stamp lands at or just before the print.
	quadboreSelfHitBackMs = 500

	// quadboreCoVictimMs pairs a same-weapon kill by the same player with
	// the quadbore — the same rocket took someone else with them.
	quadboreCoVictimMs = 100

	// telefragBoundedMatchMs pairs a frag-log telefrag with its
	// Damage.Telefrags row (KTX-instrumented demos), whose bounded value is
	// KTX's own armor+health figure — kept on the victim row as a
	// cross-check on the stream snapshot.
	telefragBoundedMatchMs = 200

	// highlightPlaceholder is the frag log's name for the party a
	// team-kill obituary did not name (result.FragResult.Unpaired).
	highlightPlaceholder = "teammate"
)

// HighlightKinds is the closed vocabulary of HighlightEvent.Kind / the
// kinds= filter, in the order the lists are published.
var HighlightKinds = []string{"discharge", "quadbore", "telefrag", "airgib"}

// HighlightsOptions parameterizes ComputeHighlights / FilterHighlights.
type HighlightsOptions struct {
	// AirgibPreMs is the airgib pre-hit look-back, handed to the detector
	// (see AirgibsOptions.PreMs): 0 = the default look-back (what the
	// post-processor bakes into the stored Result), negative = gate off.
	AirgibPreMs int32
	// Kinds keeps only these lists (FilterHighlights); empty = all four.
	Kinds []string
	// Players keeps only events whose actor or a victim is one of these
	// names (FilterHighlights); empty = every event.
	Players []string
}

// HighlightsEnvelope wraps the /highlights body (ms-native). All four lists
// are always present — [] when the match had none of that kind or the
// kinds= filter excluded it — so a consumer never distinguishes "absent"
// from "empty" by key presence; Kinds echoes the filter that applied.
// PreMs echoes the airgib pre-hit look-back the airgib list was computed
// with (0 = gate off).
type HighlightsEnvelope struct {
	TimeUnit   TimeUnit                `json:"timeUnit"`
	Kinds      []string                `json:"kinds"`
	PreMs      int                     `json:"preMs"`
	Discharges []result.HighlightEvent `json:"discharges"`
	Quadbores  []result.HighlightEvent `json:"quadbores"`
	Telefrags  []result.HighlightEvent `json:"telefrags"`
	Airgibs    []result.HighlightEvent `json:"airgibs"`
}

// ValidateHighlightKinds range-checks a caller-supplied kinds= list.
func ValidateHighlightKinds(kinds []string) error {
	return validateEnum(kinds, HighlightKinds, "kinds")
}

// Highlights returns the stored highlight catalogue. ErrUnavailable when
// the section is absent (no match streams / no frag log to build it from);
// a present section with empty lists is a measured empty.
func Highlights(r *result.Result) (*result.HighlightsResult, error) {
	if r == nil || r.Highlights == nil {
		return nil, ErrUnavailable
	}
	return r.Highlights, nil
}

// FilterHighlights applies the kinds / players selection to a catalogue
// and returns the envelope. Lists the filter excludes come back empty,
// never nil. A kinds token outside HighlightKinds is an error (the caller
// validates first with ValidateHighlightKinds; this is the backstop).
func FilterHighlights(h *result.HighlightsResult, opts HighlightsOptions, preMs int) (HighlightsEnvelope, error) {
	if err := ValidateHighlightKinds(opts.Kinds); err != nil {
		return HighlightsEnvelope{}, err
	}
	want := make(map[string]bool, len(HighlightKinds))
	if len(opts.Kinds) == 0 {
		for _, k := range HighlightKinds {
			want[k] = true
		}
	} else {
		for _, k := range opts.Kinds {
			want[strings.ToLower(strings.TrimSpace(k))] = true
		}
	}
	pf := newPlayerFilter(opts.Players)
	pick := func(kind string, in []result.HighlightEvent) []result.HighlightEvent {
		out := []result.HighlightEvent{}
		if !want[kind] {
			return out
		}
		for _, e := range in {
			if pf.accepts(e.Actor.Name) {
				out = append(out, e)
				continue
			}
			for _, v := range e.Victims {
				if pf.accepts(v.Name) {
					out = append(out, e)
					break
				}
			}
		}
		return out
	}
	kinds := make([]string, 0, len(HighlightKinds))
	for _, k := range HighlightKinds {
		if want[k] {
			kinds = append(kinds, k)
		}
	}
	env := HighlightsEnvelope{TimeUnit: UnitMs, Kinds: kinds, PreMs: preMs}
	if h != nil {
		env.Discharges = pick("discharge", h.Discharges)
		env.Quadbores = pick("quadbore", h.Quadbores)
		env.Telefrags = pick("telefrag", h.Telefrags)
		env.Airgibs = pick("airgib", h.Airgibs)
	} else {
		env.Discharges, env.Quadbores, env.Telefrags, env.Airgibs = []result.HighlightEvent{}, []result.HighlightEvent{}, []result.HighlightEvent{}, []result.HighlightEvent{}
	}
	return env, nil
}

// ComputeHighlights builds the highlight catalogue from an assembled
// Result: the final frag log (recovered team telefrags included), the
// per-player streams for every participant's state, and the damage log
// when the demo has one (KTX wire or reconstructed — the discharge and
// telefrag joins read the published entries, which both producers emit
// in the same shape). A pure function, so the REST layer can re-run it
// per request (the airgib look-back) and the post-processor can bake the
// default into the stored Result.
//
// Returns nil when there is nothing to build from: no streams (no match),
// no frag log, or no timeline analysis (the userid index and loc table
// live there). A non-nil result with empty lists is a measured empty.
func ComputeHighlights(r *result.Result, opts HighlightsOptions) *result.HighlightsResult {
	if r == nil || r.Streams == nil || r.Frags == nil || r.TimelineAnalysis == nil {
		return nil
	}
	c := newHighlightCtx(r)
	out := &result.HighlightsResult{
		Discharges: c.discharges(),
		Quadbores:  c.quadbores(),
		Telefrags:  c.telefrags(),
	}
	out.Airgibs = c.airgibs(ComputeAirgibs(r, AirgibsOptions{PreMs: opts.AirgibPreMs}))
	return out
}

// highlightCtx is the per-Result lookup state the four detectors share.
type highlightCtx struct {
	r        *result.Result
	streams  map[string]*result.PlayerStream
	userIDs  *streamUserIDIndex
	locTable []string
	frags    []result.FragEntry // r.Frags.Frags, time-ordered
}

func newHighlightCtx(r *result.Result) *highlightCtx {
	c := &highlightCtx{
		r:        r,
		streams:  make(map[string]*result.PlayerStream, len(r.Streams.Players)),
		userIDs:  newStreamUserIDIndex(r.Streams.Players, r.TimelineAnalysis.PlayerUserIDs),
		locTable: r.TimelineAnalysis.LocTable,
		frags:    r.Frags.Frags,
	}
	for i := range r.Streams.Players {
		p := &r.Streams.Players[i]
		c.streams[p.Name] = p
	}
	return c
}

// damageEvents returns the published damage log, or nil when the demo has
// none. r.Damage may be nil even though the highlights node binds
// damage:final — the recon node provides that artifact unconditionally
// and simply leaves the section absent when it has nothing to say.
func (c *highlightCtx) damageEvents() []result.DamageEntry {
	if c.r.Damage == nil {
		return nil
	}
	return c.r.Damage.Events
}

// teamOf answers a name's team from its stream; "" when unknown.
func (c *highlightCtx) teamOf(name string) string {
	if ps := c.streams[name]; ps != nil {
		return ps.Team
	}
	return ""
}

// relation classifies name against the actor: "self", "team" or "enemy".
// teamKill forces "team" for the rows the obituary itself asserted as a
// team kill (a placeholder party has no stream to compare).
func (c *highlightCtx) relation(actor, name string, teamKill bool) string {
	switch {
	case name == actor:
		return "self"
	case teamKill:
		return "team"
	}
	at, nt := c.teamOf(actor), c.teamOf(name)
	if at != "" && at == nt {
		return "team"
	}
	return "enemy"
}

// snapshot reads name's state just before tMs (see the snapshot constants
// above). A name with no stream — the "teammate" placeholder, or a party
// the streams never saw — gets the identity fields only (StateSource "").
func (c *highlightCtx) snapshot(name string, tMs int32, relation string) result.HighlightPlayer {
	p := result.HighlightPlayer{Name: name, Relation: relation}
	ps := c.streams[name]
	if ps == nil {
		return p
	}
	p.Team = ps.Team
	p.UserID = c.userIDs.at(name, tMs)

	q := tMs - highlightSnapshotLeadMs
	// The player's latest spawn at or before tMs decides whether a value
	// sample is this life's or the previous corpse's: KTX writes the spawn
	// stats in the spawn frame, so a sample OLDER than the spawn predates
	// this life (the same-frame spawn telefrag, the deflect on spawn, the
	// match-start telefrag with no sample at all). Those read the spawn
	// state — 100 health, no armor.
	lastSpawn, hasSpawn := latestAtOrBefore(ps.Spawns, tMs)
	stale := func(sampleT int32) bool { return hasSpawn && sampleT < lastSpawn }

	hi := indexI16AtOrBefore(ps.Health, q)
	if hi < 0 || stale(ps.Health[hi].T) {
		p.StateSource = "spawn"
		h, a := int16(100), int16(0)
		p.Health, p.Armor = &h, &a
	} else {
		p.StateSource = "stream"
		h := ps.Health[hi].V
		p.Health = &h
		a := int16(0)
		if ai := indexI16AtOrBefore(ps.Armor, q); ai >= 0 && !stale(ps.Armor[ai].T) {
			a = ps.Armor[ai].V
		}
		p.Armor = &a
		if ti := indexStrAtOrBefore(ps.ArmorType, q); ti >= 0 && !stale(ps.ArmorType[ti].T) && a > 0 {
			p.ArmorType = ps.ArmorType[ti].V
		}
	}
	stack := int(*p.Health) + int(*p.Armor)
	p.Stack = &stack

	lo := tMs - highlightIntervalTolMs
	for _, w := range []struct {
		code string
		ivs  []result.Interval
	}{{"rl", ps.RL}, {"lg", ps.LG}, {"gl", ps.GL}, {"ssg", ps.SSG}, {"sng", ps.SNG}} {
		if intervalOverlaps(w.ivs, lo, tMs) {
			p.Weapons = append(p.Weapons, w.code)
		}
	}
	for _, pu := range []struct {
		code string
		ivs  []result.Interval
	}{{"quad", ps.Quad}, {"pent", ps.Pent}, {"ring", ps.Ring}} {
		if intervalOverlaps(pu.ivs, lo, tMs) {
			p.Powerups = append(p.Powerups, pu.code)
		}
	}
	if ai := indexI16AtOrBefore(ps.ActiveWeapon, q); ai >= 0 {
		p.ActiveWeapon = activeWeaponName(ps.ActiveWeapon[ai].V)
	}
	if ps.Position != nil {
		if pi := nearestSampleIndex(ps.Position.T, tMs); pi >= 0 && absDeltaMs(ps.Position.T[pi], tMs) <= airgibPosMaxGapMs && pi < len(ps.Position.Li) {
			p.Loc = locNameForIndex(c.locTable, ps.Position.Li[pi])
		}
	}
	return p
}

// cellsBefore is the player's cell count just before tMs, nil without a
// sample.
func (c *highlightCtx) cellsBefore(name string, tMs int32) *int16 {
	ps := c.streams[name]
	if ps == nil {
		return nil
	}
	i := indexI16AtOrBefore(ps.Cells, tMs-highlightSnapshotLeadMs)
	if i < 0 {
		return nil
	}
	v := ps.Cells[i].V
	return &v
}

// activeWeaponName decodes a STAT_ACTIVEWEAPON IT_* bit (see
// result.PlayerStream.ActiveWeapon) into the weapon code vocabulary.
func activeWeaponName(bit int16) string {
	switch bit {
	case 1:
		return "sg"
	case 2:
		return "ssg"
	case 4:
		return "ng"
	case 8:
		return "sng"
	case 16:
		return "gl"
	case 32:
		return "rl"
	case 64:
		return "lg"
	case 4096:
		return "axe"
	}
	return ""
}

// latestAtOrBefore returns the largest value in the sorted marker list at
// or before tMs.
func latestAtOrBefore(marks []int32, tMs int32) (int32, bool) {
	i := sort.Search(len(marks), func(k int) bool { return marks[k] > tMs })
	if i == 0 {
		return 0, false
	}
	return marks[i-1], true
}

// intervalOverlaps reports whether any half-open [Start, End) interval
// overlaps the closed window [lo, hi].
func intervalOverlaps(ivs []result.Interval, lo, hi int32) bool {
	for _, iv := range ivs {
		if iv.Start <= hi && iv.End > lo {
			return true
		}
	}
	return false
}

// finish fills the counters and sorts the victims (killed first, then by
// damage, then by name) so every list reads the same way.
func finish(e *result.HighlightEvent) {
	sort.SliceStable(e.Victims, func(i, j int) bool {
		a, b := e.Victims[i], e.Victims[j]
		if a.Killed != b.Killed {
			return a.Killed
		}
		if a.Damage != b.Damage {
			return a.Damage > b.Damage
		}
		return a.Name < b.Name
	})
	e.EnemyKills, e.TeamKills = 0, 0
	for _, v := range e.Victims {
		if !v.Killed {
			continue
		}
		if v.Relation == "team" {
			e.TeamKills++
		} else {
			e.EnemyKills++
		}
	}
	if e.Sources == nil {
		e.Sources = []string{}
	}
}

func sources(frags, damage bool) []string {
	s := []string{}
	if frags {
		s = append(s, "frags")
	}
	if damage {
		s = append(s, "damage")
	}
	return s
}

// --- discharges ------------------------------------------------------------

// dischargeEvidence is one wire fact that says "this player discharged":
// an obituary with Cause "discharge" (frag != nil) or a damage-log radius
// hit from an LG (dmg != nil). The LG beam is hitscan and never splash, and
// cannot hit its own shooter, so `lg && (splash || self)` is exactly the
// discharge — KTX fires it through T_RadiusDamage (ktx/src/weapons.c:1208,
// dmg_is_splash set in combat.c:1207) and dmm4's flat self-kill is a direct
// self hit; damagerecon publishes its discharge candidates the same way.
type dischargeEvidence struct {
	t      int32
	player string
	frag   *result.FragEntry
	dmg    *result.DamageEntry
}

func (c *highlightCtx) discharges() []result.HighlightEvent {
	var ev []dischargeEvidence
	for i := range c.frags {
		f := &c.frags[i]
		if f.Cause == "discharge" {
			ev = append(ev, dischargeEvidence{t: f.Time, player: f.Killer, frag: f})
		}
	}
	dmg := c.damageEvents()
	for i := range dmg {
		d := &dmg[i]
		if d.Weapon == "lg" && (d.IsSplash || d.IsSelf) && !d.IsEnv {
			ev = append(ev, dischargeEvidence{t: d.Time, player: d.Attacker, dmg: d})
		}
	}
	if len(ev) == 0 {
		return nil
	}
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].player != ev[j].player {
			return ev[i].player < ev[j].player
		}
		return ev[i].t < ev[j].t
	})

	var out []result.HighlightEvent
	for start := 0; start < len(ev); {
		end := start + 1
		for end < len(ev) && ev[end].player == ev[start].player && ev[end].t-ev[start].t <= dischargeClusterMs {
			end++
		}
		out = append(out, c.dischargeEvent(ev[start:end]))
		start = end
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.EnemyKills != b.EnemyKills {
			return a.EnemyKills > b.EnemyKills
		}
		if a.Damage != b.Damage {
			return a.Damage > b.Damage
		}
		return a.Time < b.Time
	})
	return out
}

func (c *highlightCtx) dischargeEvent(cluster []dischargeEvidence) result.HighlightEvent {
	player, t := cluster[0].player, cluster[0].t
	e := result.HighlightEvent{Kind: "discharge", Time: t}
	e.Actor = c.snapshot(player, t, "self")
	e.Cells = c.cellsBefore(player, t)

	victims := map[string]*result.HighlightPlayer{}
	victim := func(name string, teamKill bool) *result.HighlightPlayer {
		if v := victims[name]; v != nil {
			return v
		}
		var v result.HighlightPlayer
		if name == highlightPlaceholder {
			v = result.HighlightPlayer{Name: name, Relation: "team"}
		} else {
			v = c.snapshot(name, t, c.relation(player, name, teamKill))
		}
		victims[name] = &v
		return &v
	}
	fromFrags, fromDamage := false, false
	boundedBy := map[string]int{}
	for _, x := range cluster {
		switch {
		case x.frag != nil:
			fromFrags = true
			if x.frag.IsSuicide || x.frag.Victim == player {
				e.Actor.Killed = true
			} else {
				victim(x.frag.Victim, x.frag.IsTeamKill).Killed = true
			}
		case x.dmg != nil:
			fromDamage = true
			if x.dmg.IsSelf || x.dmg.Victim == player {
				e.Actor.Damage += x.dmg.Damage
			} else {
				v := victim(x.dmg.Victim, x.dmg.IsTeam)
				v.Damage += x.dmg.Damage
				e.Damage += x.dmg.Damage
				b := x.dmg.Damage
				if x.dmg.Bounded != nil {
					b = *x.dmg.Bounded
				}
				boundedBy[x.dmg.Victim] += b
			}
		}
	}
	// A discharge that kills a TEAMMATE prints one of KTX's cause-less
	// team-kill lines ("X mows down a teammate", ktx/src/client.c:5386-5408;
	// only dtTELE1 / dtSQUISH keep their cause in the team branch, :5340-5365),
	// so the victim is only linkable by coincidence with the discharger's
	// own discharge evidence: a same-killer "teamkill" row inside the
	// cluster window is this discharge's team kill. The paired rows name
	// the victim (analyzer frag.go recoverTeamkills); an unpaired one
	// names only the killer, so its victim stays the placeholder — the
	// death is on the wire either way.
	lo, hi := t-dischargeClusterMs, t+dischargeClusterMs
	for i := range c.frags {
		f := &c.frags[i]
		if f.Weapon == "teamkill" && f.Killer == player && f.Time >= lo && f.Time <= hi && f.Victim != player {
			fromFrags = true
			victim(f.Victim, true).Killed = true
		}
	}
	for i := range c.r.Frags.Unpaired {
		f := &c.r.Frags.Unpaired[i]
		if f.Weapon == "teamkill" && f.Killer == player && f.Time >= lo && f.Time <= hi {
			fromFrags = true
			victim(highlightPlaceholder, true).Killed = true
		}
	}
	for name, v := range victims {
		e.Victims = append(e.Victims, *v)
		// The bounded enemy/team split counts every victim hit, killed
		// or not — the toplist's "given damage" columns.
		if b := boundedBy[name]; b > 0 {
			if v.Relation == "team" {
				e.DamageTeam += b
			} else {
				e.DamageEnemy += b
			}
		}
	}
	e.Sources = sources(fromFrags, fromDamage)
	finish(&e)
	return e
}

// --- quadbores -------------------------------------------------------------

func (c *highlightCtx) quadbores() []result.HighlightEvent {
	var out []result.HighlightEvent
	dmg := c.damageEvents()
	for i := range c.frags {
		f := &c.frags[i]
		if !f.IsSuicide || (f.Weapon != "rl" && f.Weapon != "gl") {
			continue
		}
		ps := c.streams[f.Victim]
		if ps == nil {
			continue
		}
		t := f.Time
		var quad *result.Interval
		for k := range ps.Quad {
			iv := &ps.Quad[k]
			if iv.Start <= t && iv.End > t-highlightIntervalTolMs {
				quad = iv
				break
			}
		}
		if quad == nil {
			continue
		}
		e := result.HighlightEvent{Kind: "quadbore", Time: t, Weapon: f.Weapon}
		e.Actor = c.snapshot(f.Victim, t, "self")
		e.Actor.Killed = true
		if held := t - quad.Start; held > 0 {
			e.QuadHeldMs = held
		}
		fromDamage := false
		for k := range c.frags {
			g := &c.frags[k]
			if g.Killer != f.Victim || g.IsSuicide || g.Victim == f.Victim {
				continue
			}
			if g.Time >= quad.Start && g.Time < t {
				e.QuadFrags++
			}
			if g.Weapon == f.Weapon && absDeltaMs(g.Time, t) <= quadboreCoVictimMs {
				v := c.snapshot(g.Victim, t, c.relation(f.Victim, g.Victim, g.IsTeamKill))
				v.Killed = true
				e.Victims = append(e.Victims, v)
			}
		}
		for k := range dmg {
			d := &dmg[k]
			if d.IsSelf && d.Weapon == f.Weapon && d.Time >= t-quadboreSelfHitBackMs && d.Time <= t && d.Attacker == f.Victim {
				fromDamage = true
				if d.Damage > e.Actor.Damage {
					e.Actor.Damage = d.Damage
				}
			}
		}
		e.Sources = sources(true, fromDamage)
		finish(&e)
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].QuadHeldMs != out[j].QuadHeldMs {
			return out[i].QuadHeldMs < out[j].QuadHeldMs
		}
		return out[i].Time < out[j].Time
	})
	return out
}

// --- telefrags -------------------------------------------------------------

func (c *highlightCtx) telefrags() []result.HighlightEvent {
	var out []result.HighlightEvent
	for i := range c.frags {
		if c.frags[i].Weapon == "tele" {
			out = append(out, c.telefragEvent(&c.frags[i]))
		}
	}
	// The unpaired rows are the victim-named team telefrags whose killer
	// neither co-location nor the frag penalty resolved: the death is on
	// the wire, only the teleporter's name is not.
	for i := range c.r.Frags.Unpaired {
		if c.r.Frags.Unpaired[i].Weapon == "tele" {
			out = append(out, c.telefragEvent(&c.r.Frags.Unpaired[i]))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		as, bs := telefragStack(a), telefragStack(b)
		if as != bs {
			return as > bs
		}
		return a.Time < b.Time
	})
	return out
}

// telefragStack is the ranking scalar: the killed victim's stack, or -1
// when the row has no killed victim (a deflect / spawnicide) so those sort
// after every real telefrag.
func telefragStack(e result.HighlightEvent) int {
	for _, v := range e.Victims {
		if v.Killed && v.Stack != nil {
			return *v.Stack
		}
	}
	return -1
}

func (c *highlightCtx) telefragEvent(f *result.FragEntry) result.HighlightEvent {
	t := f.Time
	e := result.HighlightEvent{Kind: "telefrag", Time: t, TeleKind: f.Cause}
	if e.TeleKind == "" {
		e.TeleKind = "telefrag"
	}
	if f.IsSuicide {
		// The teleporter died: onto a pentagram holder (deflect) or onto
		// a spawn (spawnicide). Sources is the frag log alone — KTX books
		// neither as damage.
		e.Actor = c.snapshot(f.Victim, t, "self")
		e.Actor.Killed = true
		e.Sources = sources(true, false)
		if e.TeleKind == "deflect" {
			if holder := c.pentHolder(f, t); holder != "" {
				v := c.snapshot(holder, t, c.relation(f.Victim, holder, false))
				v.Survived = true
				e.Victims = append(e.Victims, v)
			}
		}
		finish(&e)
		return e
	}
	// A killer the log could not name stays the placeholder: the row has
	// the victim, the time and the team relation, and nothing more.
	if f.Killer == highlightPlaceholder {
		e.Actor = result.HighlightPlayer{Name: highlightPlaceholder, Relation: "self"}
	} else {
		e.Actor = c.snapshot(f.Killer, t, "self")
	}
	v := c.snapshot(f.Victim, t, c.relation(f.Killer, f.Victim, f.IsTeamKill))
	v.Killed = true
	fromDamage := false
	if c.r.Damage != nil {
		for i := range c.r.Damage.Telefrags {
			tk := &c.r.Damage.Telefrags[i]
			if tk.Victim != f.Victim || tk.Attacker != f.Killer || absDeltaMs(tk.Time, t) > telefragBoundedMatchMs {
				continue
			}
			fromDamage = true
			if tk.Bounded != nil {
				v.Damage = *tk.Bounded
			} else {
				v.Damage = tk.Damage
			}
			break
		}
	}
	e.Victims = append(e.Victims, v)
	e.Damage = v.Damage
	e.Sources = sources(true, fromDamage)
	finish(&e)
	return e
}

// pentHolder names the pentagram holder a deflected teleporter died on:
// the obituary when it names them (dtTELE3, FragEntry.Deflector), else the
// ONE other player holding pent at the instant (dtTELE2 names nobody).
// Pent is single-instance on every stock map, so two simultaneous holders
// — the only ambiguous case — is rare, and then nobody is named rather
// than guessed.
func (c *highlightCtx) pentHolder(f *result.FragEntry, t int32) string {
	if f.Deflector != "" {
		return f.Deflector
	}
	holder := ""
	for i := range c.r.Streams.Players {
		ps := &c.r.Streams.Players[i]
		if ps.Name == f.Victim || !intervalOverlaps(ps.Pent, t-highlightIntervalTolMs, t) {
			continue
		}
		if holder != "" {
			return ""
		}
		holder = ps.Name
	}
	return holder
}

// --- airgibs ---------------------------------------------------------------

// airgibs wraps the airgib list (see ComputeAirgibs) into the shared
// shape, adding the victim's state at the hit. Order is the detector's
// (height-sorted).
func (c *highlightCtx) airgibs(list []result.AirgibEvent) []result.HighlightEvent {
	var out []result.HighlightEvent
	for _, ag := range list {
		e := result.HighlightEvent{
			Kind: "airgib", Time: ag.Time,
			Height: ag.Height, HeightAboveAttacker: ag.HeightAboveAttacker,
			Damage: ag.Damage,
		}
		e.Actor = c.snapshot(ag.Attacker, ag.Time, "self")
		v := c.snapshot(ag.Victim, ag.Time, c.relation(ag.Attacker, ag.Victim, false))
		v.Killed = ag.Lethal
		v.Damage = ag.Damage
		if ag.Loc != "" {
			// The detector's loc is the pre-impact sample's — the victim
			// as the rocket found them — which is the better answer.
			v.Loc = ag.Loc
		}
		e.Victims = append(e.Victims, v)
		e.Sources = sources(ag.Lethal, true)
		finish(&e)
		out = append(out, e)
	}
	return out
}
