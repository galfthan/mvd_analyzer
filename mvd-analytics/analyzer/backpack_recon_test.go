package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// reconFixture builds a minimal Result carrying everything the backpack
// reconstruction reads: one player stream with an active-weapon column, a
// position track, death markers, and a frag log. Every test below mutates
// one dimension of it, so the assertions pin one rule each.
type reconFixture struct {
	res *result.Result
	p   *result.PlayerStream
}

func newReconFixture() *reconFixture {
	p := result.PlayerStream{
		Name: "ace",
		Team: "red",
		// Two entries so activeWeaponLive sees a moving stat.
		ActiveWeapon: []result.ChangeI16{{T: 0, V: 1}, {T: 5000, V: 32}},
		Position: &result.PositionTrack{
			T: []int32{0, 5000, 9900},
			X: []float32{0, 100, 200},
			Y: []float32{0, 0, 10},
			Z: []float32{0, 0, 24},
		},
		Deaths: []int32{10000},
		// A weapon interval opening after match start is what
		// damagerecon.WeaponBitsLive reads as "the bits cycle".
		RL: []result.Interval{{Start: 5000, End: 10000}},
	}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{p}},
		Frags: &result.FragResult{Frags: []result.FragEntry{
			{Time: 10000, Killer: "foe", Victim: "ace", Weapon: "rl"},
		}},
		Metadata: &result.MetadataResult{ServerInfo: map[string]string{}},
	}
	return &reconFixture{res: res, p: &res.Streams.Players[0]}
}

func (f *reconFixture) drops(t *testing.T) []result.BackpackDrop {
	t.Helper()
	if reason := BackpackReconStandDown(f.res); reason != "" {
		return nil
	}
	return ReconstructBackpackDrops(f.res)
}

// The default rule (k_frp 0, ktx/src/items.c:2706): the pack holds the
// weapon the victim was WIELDING, and the hint fires only for RL and LG.
func TestBackpackRecon_WieldedWeaponDecidesTheDrop(t *testing.T) {
	for _, tc := range []struct {
		name string
		bit  int16
		want string
	}{
		{"rl", 32, "rl"},
		{"lg", 64, "lg"},
		{"shotgun drops a pack but KTX never hints it", 1, ""},
		{"ssg", 2, ""},
		{"gl", 16, ""},
		{"axe", 4096, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReconFixture()
			f.p.ActiveWeapon[1].V = tc.bit
			got := f.drops(t)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("drops = %v, want none for weapon bit %d", got, tc.bit)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("drops = %v, want exactly 1", got)
			}
			if got[0].Weapon != tc.want {
				t.Errorf("weapon = %q, want %q", got[0].Weapon, tc.want)
			}
			if got[0].Source != result.BackpackSourceReconstructed {
				t.Errorf("source = %q, want reconstructed", got[0].Source)
			}
			if got[0].EntNum != 0 {
				t.Errorf("entNum = %d, want 0 — the edict number lives only in the hint", got[0].EntNum)
			}
			if got[0].Player != "ace" || got[0].Team != "red" {
				t.Errorf("dropper = %q/%q, want ace/red", got[0].Player, got[0].Team)
			}
			if got[0].Time != 10000 {
				t.Errorf("time = %d, want the death instant 10000", got[0].Time)
			}
			// The last broadcast position (200,10,24) less the 24-unit
			// drop offset KTX applies (items.c:2703-2704).
			if got[0].Origin != [3]float32{200, 10, 0} {
				t.Errorf("origin = %v, want the last broadcast position at the pack's own height", got[0].Origin)
			}
		})
	}
}

// Inventory is not the question: a victim who OWNS the RL but is wielding
// the LG drops an LG pack. This is the case the plan's items-bits +
// priority-order sketch would have got wrong.
func TestBackpackRecon_OwnedButNotWieldedIsNotDropped(t *testing.T) {
	f := newReconFixture()
	f.p.ActiveWeapon[1].V = 64 // wielding LG
	f.p.LG = []result.Interval{{Start: 6000, End: 10000}}
	got := f.drops(t)
	if len(got) != 1 || got[0].Weapon != "lg" {
		t.Fatalf("drops = %v, want a single lg drop", got)
	}
}

// dtSUICIDE is ONLY the /kill command (ktx/src/client.c:1008), and it is the
// one deathtype DropBackpack refuses (items.c:2686-2692).
func TestBackpackRecon_KillCommandSuppressesTheDrop(t *testing.T) {
	f := newReconFixture()
	f.res.Frags.Frags[0] = result.FragEntry{
		Time: 10000, Killer: "ace", Victim: "ace", Weapon: "suicide", IsSuicide: true,
	}
	if got := f.drops(t); len(got) != 0 {
		t.Fatalf("drops = %v, want none — /kill drops no pack", got)
	}
}

// Every OTHER self-inflicted death still drops: KTX's dtRL / dtFALL /
// dtWATER_DMG paths never set dtSUICIDE, so the pack is spawned as usual.
func TestBackpackRecon_OtherSelfDeathsStillDrop(t *testing.T) {
	for _, weapon := range []string{"rl", "fall", "water", "lava", "slime", "gl", "tele"} {
		t.Run(weapon, func(t *testing.T) {
			f := newReconFixture()
			f.res.Frags.Frags[0] = result.FragEntry{
				Time: 10000, Killer: "ace", Victim: "ace", Weapon: weapon, IsSuicide: true,
			}
			if got := f.drops(t); len(got) != 1 {
				t.Fatalf("drops = %v, want 1 — only /kill suppresses the pack", got)
			}
		})
	}
}

// A stream whose display name collides with another identity's is suffixed
// "#<slot>", a form no obituary carries. The /kill suppression must still
// find it, and the emitted row must use the joinable name.
func TestBackpackRecon_DisambiguatedStreamNameStillJoins(t *testing.T) {
	// The suffix names a slot the stream itself occupied — the shape
	// disambiguatePlayerName produces.
	disambiguated := func(f *reconFixture) {
		f.p.Name = "ace#3"
		f.p.Sessions = []result.PlayerSession{{StartMs: 0, EndMs: 20000, Slot: 3, UserID: 7}}
	}

	f := newReconFixture()
	disambiguated(f)
	f.res.Frags.Frags[0] = result.FragEntry{
		Time: 10000, Killer: "ace", Victim: "ace", Weapon: "suicide", IsSuicide: true,
	}
	if got := f.drops(t); len(got) != 0 {
		t.Fatalf("drops = %v, want none — the /kill obituary names the undisambiguated player", got)
	}

	f = newReconFixture()
	disambiguated(f)
	got := f.drops(t)
	if len(got) != 1 || got[0].Player != "ace" {
		t.Fatalf("drops = %v, want one row named \"ace\"", got)
	}
}

// The other half of the same rule: a player whose REAL name ends in
// "#<digits>" keeps it. Stripping it unconditionally renamed them to a name
// the frag log, scoreboard and playerStats have never heard of.
func TestBackpackRecon_GenuineHashNameIsNotStripped(t *testing.T) {
	f := newReconFixture()
	f.p.Name = "ace#3" // no sibling stream, and slot 3 is not theirs
	f.p.Sessions = []result.PlayerSession{{StartMs: 0, EndMs: 20000, Slot: 5, UserID: 7}}
	f.res.Frags.Frags[0] = result.FragEntry{
		Time: 10000, Killer: "foe", Victim: "ace#3", Weapon: "rl",
	}
	got := f.drops(t)
	if len(got) != 1 || got[0].Player != "ace#3" {
		t.Fatalf("drops = %v, want one row still named \"ace#3\"", got)
	}
}

// Two identities rendering the same display name are BOTH suffixed, so the
// pair itself attests the disambiguation even with no session list.
func TestBackpackRecon_CollidingSuffixPairIsStripped(t *testing.T) {
	f := newReconFixture()
	f.p.Name = "ace#3"
	sibling := *f.p
	sibling.Name = "ace#6"
	sibling.Deaths = nil
	f.res.Streams.Players = append(f.res.Streams.Players, sibling)
	got := f.drops(t)
	if len(got) != 1 || got[0].Player != "ace" {
		t.Fatalf("drops = %v, want one row named \"ace\"", got)
	}
}

// /kill obituaries are CONSUMED, one death each. A membership test let a
// single obituary suppress every death inside its window — including another
// player's genuine RL death (two identities rendering the same display name)
// and the dropper's own next death after an instant respawn
// (ktx/src/client.c:2594-2597).
func TestBackpackRecon_KillCommandSuppressesOneDeathOnly(t *testing.T) {
	t.Run("same name, two identities", func(t *testing.T) {
		f := newReconFixture()
		f.p.Name = "ace#3"
		f.p.Sessions = []result.PlayerSession{{StartMs: 0, EndMs: 20000, Slot: 3, UserID: 7}}
		other := *f.p
		other.Name = "ace#6"
		other.Sessions = []result.PlayerSession{{StartMs: 0, EndMs: 20000, Slot: 6, UserID: 8}}
		other.Deaths = []int32{10300}
		f.res.Streams.Players = append(f.res.Streams.Players, other)
		// Only the first player typed /kill.
		f.res.Frags.Frags = []result.FragEntry{
			{Time: 10000, Killer: "ace", Victim: "ace", Weapon: "suicide", IsSuicide: true},
			{Time: 10300, Killer: "foe", Victim: "ace", Weapon: "rl"},
		}
		got := f.drops(t)
		if len(got) != 1 || got[0].Time != 10300 {
			t.Fatalf("drops = %v, want the second player's 10300 pack only", got)
		}
	})

	// /kill respawns the player instantly (ktx/src/client.c:2594-2597), so a
	// genuine death can land inside the same window.
	t.Run("kill then a real death in the same window", func(t *testing.T) {
		f := newReconFixture()
		f.p.Deaths = []int32{10000, 10200}
		f.res.Frags.Frags = []result.FragEntry{
			{Time: 10000, Killer: "ace", Victim: "ace", Weapon: "suicide", IsSuicide: true},
			{Time: 10200, Killer: "foe", Victim: "ace", Weapon: "rl"},
		}
		got := f.drops(t)
		if len(got) != 1 || got[0].Time != 10200 {
			t.Fatalf("drops = %v, want the 10200 pack only — the /kill accounts for 10000", got)
		}
	})

	t.Run("two kills suppress two deaths", func(t *testing.T) {
		f := newReconFixture()
		f.p.Deaths = []int32{10000, 10200}
		f.res.Frags.Frags = []result.FragEntry{
			{Time: 10000, Killer: "ace", Victim: "ace", Weapon: "suicide", IsSuicide: true},
			{Time: 10200, Killer: "ace", Victim: "ace", Weapon: "suicide", IsSuicide: true},
		}
		if got := f.drops(t); len(got) != 0 {
			t.Fatalf("drops = %v, want none — both deaths were /kill", got)
		}
	})
}

// A single-valued aw column is NOT the frozen-stat signature, and must not
// be refused as one. In a single-weapon ruleset — `1on1-lgc`, `2on2-midair`,
// rocket arena on end/endif — the wielded weapon cannot change, so every
// player carries exactly one sample and KTX still drops a pack on every
// death. Measured on the ground-truth sample, a per-player "this column
// never moves" refusal cost 58 of 13 749 hint-confirmed drops and prevented
// no fabrication; the demo-level gate's `|| WeaponBitsLive` arm is what keeps
// such demos measurable at all.
func TestBackpackRecon_SingleWeaponRulesetStillDrops(t *testing.T) {
	f := newReconFixture()
	// The whole match on one weapon: one aw sample, several deaths.
	f.p.ActiveWeapon = []result.ChangeI16{{T: 0, V: 32}}
	f.p.Deaths = []int32{9950, 10000}
	f.res.Frags.Frags = []result.FragEntry{
		{Time: 9950, Killer: "foe", Victim: "ace", Weapon: "rl"},
		{Time: 10000, Killer: "foe", Victim: "ace", Weapon: "rl"},
	}
	got := f.drops(t)
	if len(got) != 2 {
		t.Fatalf("drops = %v, want a pack on both deaths — a ruleset weapon never moves", got)
	}
}

// The wielded weapon is delta-coded against a per-SLOT cache mvdsv never
// resets across client changes (sv_send.c:1279-1281), so a player who
// reconnects onto ANOTHER slot can have no aw sample of their own on it. The
// answer is "unknown", not the weapon they held minutes ago on the slot they
// left. A reconnect onto the same slot keeps the cache — and the sample.
func TestBackpackRecon_StaleWeaponAcrossAReconnectIsRefused(t *testing.T) {
	withSessions := func(slot2 int) *reconFixture {
		f := newReconFixture()
		f.p.Sessions = []result.PlayerSession{
			{StartMs: 0, EndMs: 8000, Slot: 3, UserID: 7},
			{StartMs: 9000, EndMs: 20000, Slot: slot2, UserID: 7},
		}
		return f
	}

	// Both aw samples belong to the first connection, on another slot.
	f := withSessions(6)
	f.p.ActiveWeapon = []result.ChangeI16{{T: 0, V: 1}, {T: 5000, V: 32}}
	if got := f.drops(t); len(got) != 0 {
		t.Fatalf("drops = %v, want none — the newest aw sample was carried on a different slot", got)
	}

	// A sample inside the current connection is trusted as usual.
	f = withSessions(6)
	f.p.ActiveWeapon = []result.ChangeI16{{T: 0, V: 1}, {T: 9500, V: 32}}
	if got := f.drops(t); len(got) != 1 {
		t.Fatalf("drops = %v, want 1 — the aw sample is on the slot the death is on", got)
	}

	// Same slot both times: the per-slot cache never lapsed, so the earlier
	// sample is this client's own last report and still answers.
	f = withSessions(3)
	f.p.ActiveWeapon = []result.ChangeI16{{T: 0, V: 1}, {T: 5000, V: 32}}
	if got := f.drops(t); len(got) != 1 {
		t.Fatalf("drops = %v, want 1 — a same-slot reconnect does not stale the stat", got)
	}
}

// The drop origin is the victim's last broadcast position. When the track
// has gone stale past the bound the drop is withheld, not centred on a
// guess.
func TestBackpackRecon_StalePositionWithholdsTheDrop(t *testing.T) {
	f := newReconFixture()
	f.p.Position.T = []int32{0, 5000, 9000} // 1000 ms before the death
	if got := f.drops(t); len(got) != 0 {
		t.Fatalf("drops = %v, want none — position staler than %dms", got, backpackPosStaleMs)
	}

	f = newReconFixture()
	f.p.Position = nil
	if got := f.drops(t); len(got) != 0 {
		t.Fatalf("drops = %v, want none — no position track at all", got)
	}
}

func TestBackpackReconStandDown(t *testing.T) {
	t.Run("frozen weapon state", func(t *testing.T) {
		f := newReconFixture()
		// One active-weapon sample and one [0,end) inventory interval per
		// weapon is the frozen-recorder signature both detectors refuse.
		f.p.ActiveWeapon = []result.ChangeI16{{T: 0, V: 32}}
		f.p.RL = []result.Interval{{Start: 0, End: 10000}}
		if got := BackpackReconStandDown(f.res); got != "frozen weapon state" {
			t.Errorf("stand-down = %q, want frozen weapon state", got)
		}
	})
	t.Run("no active-weapon stat", func(t *testing.T) {
		f := newReconFixture()
		f.p.ActiveWeapon = nil
		if got := BackpackReconStandDown(f.res); got != "no active-weapon stat" {
			t.Errorf("stand-down = %q, want no active-weapon stat", got)
		}
	})
	t.Run("no frag log", func(t *testing.T) {
		f := newReconFixture()
		f.res.Frags = nil
		if got := BackpackReconStandDown(f.res); got != "no frag log" {
			t.Errorf("stand-down = %q, want no frag log", got)
		}
	})
	t.Run("no player streams", func(t *testing.T) {
		f := newReconFixture()
		f.res.Streams = nil
		if got := BackpackReconStandDown(f.res); got != "no player streams" {
			t.Errorf("stand-down = %q, want no player streams", got)
		}
	})
	t.Run("hinting mod", func(t *testing.T) {
		// KTX >= 1.38 emits the hint itself, so an empty section there is
		// the wire's answer, not a gap to fill.
		f := newReconFixture()
		f.res.Metadata.ServerInfo["ktxver"] = "1.46-dev-r402"
		if got := BackpackReconStandDown(f.res); got != "hinting mod emitted no drops" {
			t.Errorf("stand-down = %q, want hinting mod emitted no drops", got)
		}
	})
	t.Run("pre-hint ktx proceeds", func(t *testing.T) {
		f := newReconFixture()
		f.res.Metadata.ServerInfo["ktxver"] = "1.37"
		if got := BackpackReconStandDown(f.res); got != "" {
			t.Errorf("stand-down = %q, want none on a pre-1.38 KTX", got)
		}
	})
	for _, tc := range []struct {
		name string
		set  func(*reconFixture)
		want string
	}{
		{"bloodfest cvar", func(f *reconFixture) { f.res.Metadata.ServerInfo["k_bloodfest"] = "1" }, "mode:bloodfest"},
		{"bloodfest submode", func(f *reconFixture) { f.res.Metadata.ServerInfo["mode"] = "ffa-bf" }, "mode:bloodfest"},
		{"yawnmode submode", func(f *reconFixture) { f.res.Metadata.ServerInfo["mode"] = "1on1-yw" }, "mode:yawnmode"},
		{"yawnmode countdown", func(f *reconFixture) {
			f.res.Metadata.MatchSettings = &result.MatchSettings{Yawnmode: true}
		}, "mode:yawnmode"},
		{"fairpacks broadcast", func(f *reconFixture) {
			f.res.Metadata.MatchSettings = &result.MatchSettings{Fairpacks: "best weapon"}
		}, "mode:fairpacks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReconFixture()
			tc.set(f)
			if got := BackpackReconStandDown(f.res); got != tc.want {
				t.Errorf("stand-down = %q, want %q", got, tc.want)
			}
		})
	}
	// Modes that rewrite T_Damage but leave DropBackpack alone must NOT
	// suppress drops — there is no such early return in items.c, and the
	// wire hints prove packs drop in them.
	for _, mode := range []string{"1on1-midair", "1on1-instagib", "4on4-df", "ffa-ca", "ffa-wo"} {
		t.Run("damage-only mode "+mode, func(t *testing.T) {
			f := newReconFixture()
			f.res.Metadata.ServerInfo["mode"] = mode
			if got := BackpackReconStandDown(f.res); got != "" {
				t.Errorf("stand-down = %q, want none for %q", got, mode)
			}
		})
	}
}

// The wire hint always wins: a demo that carried `//ktx drop` is never
// overwritten, and the two provenances are never mixed.
func TestBackpackReconPost_WireHintsWin(t *testing.T) {
	f := newReconFixture()
	f.res.Backpacks = []result.BackpackDrop{
		{Time: 1, Player: "ace", Weapon: "lg", EntNum: 42, Source: result.BackpackSourceKTX},
	}
	backpackReconPost(f.res, &CoreOutputs{})
	if len(f.res.Backpacks) != 1 || f.res.Backpacks[0].Source != result.BackpackSourceKTX {
		t.Fatalf("backpacks = %v, want the single wire-hinted row untouched", f.res.Backpacks)
	}
}

func TestBackpackReconPost_FillsAbsentSection(t *testing.T) {
	f := newReconFixture()
	backpackReconPost(f.res, &CoreOutputs{})
	if len(f.res.Backpacks) != 1 || f.res.Backpacks[0].Source != result.BackpackSourceReconstructed {
		t.Fatalf("backpacks = %v, want one reconstructed row", f.res.Backpacks)
	}
}

func TestKtxVersionNumber(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"1.46-dev-r402", 146},
		{"1.40-beta-quakecon-release3", 140},
		{"1.38", 138},
		{"1.4", 140}, // a one-digit minor is tenths, not hundredths
		{"1.9", 190},
		{"2.00", 200},
		{"nonsense", 0},
		{"1", 0},
	} {
		if got := ktxVersionNumber(tc.in); got != tc.want {
			t.Errorf("ktxVersionNumber(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestUndisambiguatedName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ace", "ace"},
		{"fe4r#7", "fe4r"},
		{"fe4r#", "fe4r#"},
		{"#7", "#7"},
		{"we#1rd", "we#1rd"},
	} {
		if got := undisambiguatedName(tc.in); got != tc.want {
			t.Errorf("undisambiguatedName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The active-weapon column is the one stat recorded through the countdown:
// STAT_ACTIVEWEAPON is delta-coded, so a player who last switched weapons in
// warmup would otherwise have no in-match sample at all. The rebase keeps the
// latest pre-match value as the carry-forward "value at t=0".
func TestTimelineActiveWeaponRecordedThroughWarmup(t *testing.T) {
	a := NewTimelineAnalyzer()
	_ = a.Init(&Context{})

	// Warmup: the player is wielding the RL. No match-start print yet.
	_ = a.handleStatUpdate(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatActiveWeapon, Value: 32, TimeMs: 1000})
	// Health during warmup is still ignored — the gate only lifted for the
	// wielded weapon.
	_ = a.handleStatUpdate(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 250, TimeMs: 1000})
	a.timing.Started = true
	_ = a.handleStatUpdate(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatActiveWeapon, Value: 1, TimeMs: 5000})

	b := &a.playerState[0].streams
	if len(b.health) != 0 {
		t.Errorf("health = %v, want nothing recorded during warmup", b.health)
	}
	if len(b.activeWeapon) != 2 || b.activeWeapon[0].v != 32 || b.activeWeapon[1].v != 1 {
		t.Fatalf("activeWeapon = %v, want the warmup RL then the match-start SG", b.activeWeapon)
	}

	ps := b.toPlayerStream("p", "")
	got := shiftAndFilterChangeI16(ps.ActiveWeapon, 4000)
	if len(got) != 2 || got[0].T != 0 || got[0].V != 32 || got[1].T != 1000 || got[1].V != 1 {
		t.Errorf("rebased = %v, want the pre-match value carried to t=0", got)
	}
}

// A mod sentinel outside the IT_* weapon range never reaches the column as a
// plausible weapon bit.
func TestTimelineActiveWeaponRejectsOutOfRange(t *testing.T) {
	a := NewTimelineAnalyzer()
	_ = a.Init(&Context{})
	a.timing.Started = true
	for _, v := range []int{-1, 4097, 100000} {
		_ = a.handleStatUpdate(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatActiveWeapon, Value: v, TimeMs: 1000})
	}
	if st := a.playerState[0]; st != nil && len(st.streams.activeWeapon) != 0 {
		t.Errorf("activeWeapon = %v, want nothing recorded", st.streams.activeWeapon)
	}
}
