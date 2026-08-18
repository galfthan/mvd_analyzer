package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// linkFixture builds the smallest Result the pickup linkage reads: two player
// streams with position, liveness and RL possession, plus one reconstructed
// drop and one pack-entity life to bind it to. Each test moves ONE dimension,
// so every assertion pins one rule.
//
// Geometry convention throughout: the pack sits at (0,0,0) — a pack origin is
// at its own feet — and a player standing on it is at z=24, their origin
// being 24 above their feet.
type linkFixture struct {
	res   *result.Result
	packs []PackEntityLife
	drops []result.BackpackDrop
}

func newLinkFixture() *linkFixture {
	mk := func(name, team string, x float32) result.PlayerStream {
		return result.PlayerStream{
			Name: name,
			Team: team,
			Position: &result.PositionTrack{
				T: []int32{9000, 10000, 20000, 20033},
				X: []float32{x, x, x, x},
				Y: []float32{0, 0, 0, 0},
				Z: []float32{24, 24, 24, 24},
			},
			Alive: []result.Interval{{Start: 0, End: 60000}},
		}
	}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			mk("ace", "red", 1000), // far away unless a test moves them
			mk("foe", "blue", 2000),
		}},
	}
	return &linkFixture{
		res: res,
		packs: []PackEntityLife{{
			EntNum: 205,
			Start:  10000,
			Spawn:  [3]float32{0, 0, 0},
			End:    20033,
			Rest:   [3]float32{0, 0, 0},
			Ended:  true,
		}},
		drops: []result.BackpackDrop{{
			Time:   10000,
			Player: "victim",
			Weapon: "rl",
			Origin: [3]float32{0, 0, 0},
			Source: result.BackpackSourceReconstructed,
		}},
	}
}

// onPack parks a player on the pack for the whole track.
func (f *linkFixture) onPack(i int) {
	p := &f.res.Streams.Players[i]
	for j := range p.Position.X {
		p.Position.X[j], p.Position.Y[j], p.Position.Z[j] = 0, 0, 24
	}
}

func (f *linkFixture) link() []result.BackpackDrop {
	LinkBackpackDrops(f.res, f.packs, f.drops)
	return f.drops
}

// The bind is by time AND place: the pack that appears at the drop instant
// nearest to where DropBackpack put it.
func TestBackpackLinkage_BindsTheNearestPackAtTheDropInstant(t *testing.T) {
	f := newLinkFixture()
	// A second pack appears in the same frame 300 units away — another
	// player dying elsewhere.
	f.packs = append(f.packs, PackEntityLife{
		EntNum: 301, Start: 10000, Spawn: [3]float32{300, 0, 0},
		End: 15000, Rest: [3]float32{300, 0, 0}, Ended: true,
	})
	got := f.link()
	if got[0].EntNum != 205 {
		t.Errorf("entNum = %d, want 205 (the pack at the drop position)", got[0].EntNum)
	}
}

func TestBackpackLinkage_TwoDropsInOneFrameTakeTheirOwnPacks(t *testing.T) {
	f := newLinkFixture()
	f.packs = append(f.packs, PackEntityLife{
		EntNum: 301, Start: 10000, Spawn: [3]float32{300, 0, 0},
		End: 15000, Rest: [3]float32{300, 0, 0}, Ended: true,
	})
	f.drops = append(f.drops, result.BackpackDrop{
		Time: 10000, Player: "other", Weapon: "lg",
		Origin: [3]float32{300, 0, 0}, Source: result.BackpackSourceReconstructed,
	})
	got := f.link()
	if got[0].EntNum != 205 || got[1].EntNum != 301 {
		t.Errorf("entNums = %d, %d; want 205, 301 — each drop takes the pack it is standing on",
			got[0].EntNum, got[1].EntNum)
	}
}

// No pack near the drop is a refusal, not a nearest-anything snap.
func TestBackpackLinkage_RefusesToBindWhenNoPackIsThere(t *testing.T) {
	for _, tc := range []struct {
		name string
		pack PackEntityLife
	}{
		{"too far away", PackEntityLife{EntNum: 9, Start: 10000, Spawn: [3]float32{500, 0, 0}, Ended: true, End: 20000}},
		{"too late", PackEntityLife{EntNum: 9, Start: 11000, Spawn: [3]float32{0, 0, 0}, Ended: true, End: 20000}},
		{"too early", PackEntityLife{EntNum: 9, Start: 9000, Spawn: [3]float32{0, 0, 0}, Ended: true, End: 20000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLinkFixture()
			f.packs = []PackEntityLife{tc.pack}
			f.onPack(0)
			got := f.link()
			if got[0].EntNum != 0 {
				t.Errorf("entNum = %d, want 0 — nothing should have bound", got[0].EntNum)
			}
			if got[0].Fate != result.BackpackFateUnobserved {
				t.Errorf("fate = %q, want unobserved", got[0].Fate)
			}
			if got[0].Picker != "" {
				t.Errorf("picker = %q, want none", got[0].Picker)
			}
		})
	}
}

// Packs FALL. The pickup test runs at where the pack came to rest, not where
// it was dropped.
func TestBackpackLinkage_FollowsTheFallToTheRestingPosition(t *testing.T) {
	f := newLinkFixture()
	f.packs[0].Rest = [3]float32{0, 0, -400} // fell down a lift shaft
	f.onPack(0)                              // ace stands at the DROP position, not the rest one
	if got := f.link(); got[0].Fate != result.BackpackFateUnobserved {
		t.Fatalf("fate = %q, want unobserved — ace is 400 units above where the pack landed", got[0].Fate)
	}

	f = newLinkFixture()
	f.packs[0].Rest = [3]float32{0, 0, -400}
	p := f.res.Streams.Players[0].Position
	for j := range p.X {
		p.X[j], p.Y[j], p.Z[j] = 0, 0, -376
	}
	got := f.link()
	if got[0].Fate != result.BackpackFatePicked || got[0].Picker != "ace" {
		t.Fatalf("fate/picker = %q/%q, want picked/ace at the resting position", got[0].Fate, got[0].Picker)
	}
}

// The disappearance is classified by who was on the pack, which is the
// server's own test — not by whether anyone gained a weapon bit.
func TestBackpackLinkage_TouchAloneAttributesARedundantGrab(t *testing.T) {
	f := newLinkFixture()
	f.onPack(1)
	// foe already holds the RL for the whole match: no bit gain to read.
	f.res.Streams.Players[1].RL = []result.Interval{{Start: 0, End: 60000}}
	got := f.link()
	if got[0].Fate != result.BackpackFatePicked {
		t.Fatalf("fate = %q, want picked", got[0].Fate)
	}
	if got[0].Picker != "foe" || got[0].PickerTeam != "blue" {
		t.Errorf("picker = %q/%q, want foe/blue", got[0].Picker, got[0].PickerTeam)
	}
	if got[0].PickupTime != 20033 {
		t.Errorf("pickupTime = %d, want 20033", got[0].PickupTime)
	}
}

// The touch box is the two abs boxes overlapping, with the 15-unit FL_ITEM
// expansion mvdsv applies (sv_world.c:373-379). A player 40 units away is on
// the pack; one 60 units away is not.
func TestBackpackLinkage_TouchBoxMatchesTheEngineExpansion(t *testing.T) {
	for _, tc := range []struct {
		name string
		dx   float32
		want string
	}{
		{"under the raw 32-unit box", 20, result.BackpackFatePicked},
		{"inside only because of the FL_ITEM expansion", 40, result.BackpackFatePicked},
		{"outside", 60, result.BackpackFateUnobserved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLinkFixture()
			p := f.res.Streams.Players[0].Position
			for j := range p.X {
				p.X[j], p.Y[j], p.Z[j] = tc.dx, 0, 24
			}
			if got := f.link(); got[0].Fate != tc.want {
				t.Errorf("fate at dx=%.0f = %q, want %q", tc.dx, got[0].Fate, tc.want)
			}
		})
	}
}

// The touch instant falls between two broadcasts, so the path between them is
// what is tested — a player who is outside the box at both ends but crossed
// it in between took the pack.
func TestBackpackLinkage_SweepsThePathBetweenBroadcasts(t *testing.T) {
	f := newLinkFixture()
	p := f.res.Streams.Players[0].Position
	// Two samples bracketing the disappearance at 20033, on opposite sides.
	p.T = []int32{20000, 20066}
	p.X = []float32{-60, 60}
	p.Y = []float32{0, 0}
	p.Z = []float32{24, 24}
	if got := f.link(); got[0].Fate != result.BackpackFatePicked || got[0].Picker != "ace" {
		t.Errorf("fate/picker = %q/%q, want picked/ace — the path crosses the pack",
			got[0].Fate, got[0].Picker)
	}
}

// A corpse keeps streaming position samples at full rate, and BackpackTouch
// returns immediately on ISDEAD (ktx/src/items.c:2377).
func TestBackpackLinkage_DeadPlayersCannotTakeAPack(t *testing.T) {
	f := newLinkFixture()
	f.onPack(0)
	f.res.Streams.Players[0].Alive = []result.Interval{{Start: 0, End: 15000}}
	if got := f.link(); got[0].Fate != result.BackpackFateUnobserved {
		t.Errorf("fate = %q, want unobserved — ace was a corpse lying on the pack", got[0].Fate)
	}
}

// KTX removes a player's pack 120 s after the drop (items.c:2871-2872).
func TestBackpackLinkage_ExpiryNeedsTheFullTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		life int32
		want string
	}{
		{"the KTX timeout", 120000, result.BackpackFateExpired},
		{"one frame short of it, within tolerance", 118500, result.BackpackFateExpired},
		{"vanished early with nobody on it", 8000, result.BackpackFateUnobserved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLinkFixture()
			f.packs[0].End = f.packs[0].Start + tc.life
			if got := f.link(); got[0].Fate != tc.want {
				t.Errorf("fate after %dms = %q, want %q", tc.life, got[0].Fate, tc.want)
			}
		})
	}
}

// A pack still on the wire when the recording stops did not expire — the
// evidence ran out first.
func TestBackpackLinkage_UnendedLifeIsUnobservedNotExpired(t *testing.T) {
	f := newLinkFixture()
	f.packs[0].Ended = false
	got := f.link()
	if got[0].Fate != result.BackpackFateUnobserved {
		t.Errorf("fate = %q, want unobserved", got[0].Fate)
	}
	if got[0].EntNum != 205 {
		t.Errorf("entNum = %d, want 205 — the bind still happened", got[0].EntNum)
	}
}

// Two players on one pack: the weapon-bit gain separates them, and only when
// it names exactly one.
func TestBackpackLinkage_TwoOnThePackAreSeparatedByTheWeaponGain(t *testing.T) {
	t.Run("one gained the weapon", func(t *testing.T) {
		f := newLinkFixture()
		f.onPack(0)
		f.onPack(1)
		f.res.Streams.Players[0].RL = []result.Interval{{Start: 0, End: 60000}}     // had it already
		f.res.Streams.Players[1].RL = []result.Interval{{Start: 20100, End: 60000}} // gained it
		got := f.link()
		if got[0].Fate != result.BackpackFatePicked {
			t.Fatalf("fate = %q, want picked", got[0].Fate)
		}
		if got[0].Picker != "foe" {
			t.Errorf("picker = %q, want foe", got[0].Picker)
		}
	})
	t.Run("neither gained the weapon", func(t *testing.T) {
		f := newLinkFixture()
		f.onPack(0)
		f.onPack(1)
		f.res.Streams.Players[0].RL = []result.Interval{{Start: 0, End: 60000}}
		f.res.Streams.Players[1].RL = []result.Interval{{Start: 0, End: 60000}}
		got := f.link()
		if got[0].Fate != result.BackpackFatePicked {
			t.Fatalf("fate = %q, want picked — the pack was taken either way", got[0].Fate)
		}
		if got[0].Picker != "" {
			t.Errorf("picker = %q, want none — the evidence names nobody", got[0].Picker)
		}
	})
	t.Run("both gained the weapon", func(t *testing.T) {
		f := newLinkFixture()
		f.onPack(0)
		f.onPack(1)
		f.res.Streams.Players[0].RL = []result.Interval{{Start: 20100, End: 60000}}
		f.res.Streams.Players[1].RL = []result.Interval{{Start: 20100, End: 60000}}
		if got := f.link(); got[0].Picker != "" {
			t.Errorf("picker = %q, want none — two gains separate nobody", got[0].Picker)
		}
	})
}

// A gain that could have come from the world spawner the player is standing
// on is not evidence about the pack.
func TestBackpackLinkage_SpawnerExclusionDisqualifiesTheTieBreak(t *testing.T) {
	f := newLinkFixture()
	f.onPack(0)
	f.onPack(1)
	f.res.Streams.Players[0].RL = []result.Interval{{Start: 0, End: 60000}}
	f.res.Streams.Players[1].RL = []result.Interval{{Start: 20100, End: 60000}}
	// The map's RL pad is right where both players are standing.
	f.res.Items = &result.ItemsResult{Items: []result.ItemTimeline{
		{Name: "rl_1", Kind: "rl", X: 0, Y: 0, Z: 0},
	}}
	got := f.link()
	if got[0].Fate != result.BackpackFatePicked {
		t.Fatalf("fate = %q, want picked", got[0].Fate)
	}
	if got[0].Picker != "" {
		t.Errorf("picker = %q, want none — foe's RL could have come off the pad", got[0].Picker)
	}
}

// The linkage answers only for rows it produced. A `ktx` row's fate is the
// weaponPickups join, and restating it here could only disagree with it.
func TestBackpackLinkage_LeavesWireHintedRowsAlone(t *testing.T) {
	f := newLinkFixture()
	f.onPack(0)
	f.drops[0].Source = result.BackpackSourceKTX
	f.drops[0].EntNum = 77
	got := f.link()
	if got[0].Fate != "" || got[0].Picker != "" {
		t.Errorf("fate/picker = %q/%q, want both empty on a wire-hinted row", got[0].Fate, got[0].Picker)
	}
	if got[0].EntNum != 77 {
		t.Errorf("entNum = %d, want the hint's own 77", got[0].EntNum)
	}
}

// The post-processor stands down on a hint-carrying demo as a whole.
func TestBackpackLinkagePost_StandsDownOnAHintCarryingDemo(t *testing.T) {
	f := newLinkFixture()
	f.onPack(0)
	f.res.Backpacks = []result.BackpackDrop{
		{Time: 10000, Player: "victim", Weapon: "rl", Origin: [3]float32{0, 0, 0}, Source: result.BackpackSourceKTX, EntNum: 205},
		{Time: 10000, Player: "other", Weapon: "rl", Origin: [3]float32{0, 0, 0}, Source: result.BackpackSourceReconstructed},
	}
	backpackLinkagePost(f.res, &CoreOutputs{PackEntities: f.packs})
	for i, b := range f.res.Backpacks {
		if b.Fate != "" {
			t.Errorf("row %d fate = %q, want empty — the demo carries wire hints", i, b.Fate)
		}
	}
}

func TestBackpackLinkagePost_RunsOnAReconstructedSection(t *testing.T) {
	f := newLinkFixture()
	f.onPack(0)
	f.res.Backpacks = f.drops
	backpackLinkagePost(f.res, &CoreOutputs{PackEntities: f.packs})
	if got := f.res.Backpacks[0]; got.Fate != result.BackpackFatePicked || got.Picker != "ace" {
		t.Errorf("fate/picker = %q/%q, want picked/ace", got.Fate, got.Picker)
	}
}

func TestBackpackLinkagePost_NoPackTrackLeavesEveryRowUnobserved(t *testing.T) {
	f := newLinkFixture()
	f.onPack(0)
	f.res.Backpacks = f.drops
	backpackLinkagePost(f.res, &CoreOutputs{})
	if got := f.res.Backpacks[0]; got.Fate != result.BackpackFateUnobserved || got.EntNum != 0 {
		t.Errorf("fate/entNum = %q/%d, want unobserved/0 on a demo with no entity stream", got.Fate, got.EntNum)
	}
}

// One edict carries many packs over a match. Each appearance is its own life,
// so a later pack's pickup is never credited to an earlier pack's drop.
func TestBackpackAnalyzer_PackLivesSplitOnEachAppearance(t *testing.T) {
	a := NewBackpackAnalyzer()
	feed := func(evs ...events.Event) {
		for _, e := range evs {
			if err := a.OnEvent(e); err != nil {
				t.Fatalf("OnEvent: %v", err)
			}
		}
	}
	feed(
		&events.ItemSpawnEvent{EntNum: 205, Kind: "backpack", Origin: [3]float32{0, 0, 100}, TimeMs: 1000},
		// The parser announces the same appearance twice: classification and
		// then visibility. One life, not two.
		&events.ItemStateEvent{EntNum: 205, Kind: "backpack", Taken: false, Origin: [3]float32{0, 0, 100}, TimeMs: 1000},
		&events.ItemMoveEvent{EntNum: 205, Kind: "backpack", Origin: [3]float32{0, 0, 40}, TimeMs: 1030},
		&events.ItemMoveEvent{EntNum: 205, Kind: "backpack", Origin: [3]float32{0, 0, 0}, TimeMs: 1060},
		&events.ItemStateEvent{EntNum: 205, Kind: "backpack", Taken: true, Origin: [3]float32{0, 0, 0}, TimeMs: 5000},
		// The edict is handed to the next pack, elsewhere on the map.
		&events.ItemStateEvent{EntNum: 205, Kind: "backpack", Taken: false, Origin: [3]float32{900, 0, 0}, TimeMs: 30000},
		&events.ItemStateEvent{EntNum: 205, Kind: "backpack", Taken: true, Origin: [3]float32{900, 0, 0}, TimeMs: 33000},
		// A non-backpack item's events never enter the track.
		&events.ItemStateEvent{EntNum: 8, Kind: "ra", Taken: true, TimeMs: 34000},
	)
	co := &CoreOutputs{}
	a.PopulateCore(co)
	if len(co.PackEntities) != 2 {
		t.Fatalf("pack lives = %d (%+v), want 2", len(co.PackEntities), co.PackEntities)
	}
	first, second := co.PackEntities[0], co.PackEntities[1]
	if first.Start != 1000 || first.End != 5000 || !first.Ended {
		t.Errorf("first life = %+v, want 1000..5000 ended", first)
	}
	if first.Spawn != ([3]float32{0, 0, 100}) || first.Rest != ([3]float32{0, 0, 0}) {
		t.Errorf("first life spawn/rest = %v/%v, want the fall tracked from z=100 to z=0", first.Spawn, first.Rest)
	}
	if first.Moves != 2 {
		t.Errorf("first life moves = %d, want 2", first.Moves)
	}
	if second.Start != 30000 || second.End != 33000 || second.Spawn != ([3]float32{900, 0, 0}) {
		t.Errorf("second life = %+v, want a separate 30000..33000 life at x=900", second)
	}
}

// A pack still up when the recording stops is published unended.
func TestBackpackAnalyzer_UnclosedPackLifeIsPublishedUnended(t *testing.T) {
	a := NewBackpackAnalyzer()
	if err := a.OnEvent(&events.ItemSpawnEvent{EntNum: 300, Kind: "backpack", Origin: [3]float32{5, 5, 5}, TimeMs: 2000}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	co := &CoreOutputs{}
	a.PopulateCore(co)
	if len(co.PackEntities) != 1 {
		t.Fatalf("pack lives = %d, want 1", len(co.PackEntities))
	}
	if co.PackEntities[0].Ended {
		t.Error("life reported as ended; the recording stopped while the pack was up")
	}
}
