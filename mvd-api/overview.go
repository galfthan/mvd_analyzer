package main

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/mapbsp"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
)

// Overview is a curated summary of a parsed *Result, cheap to compute
// from existing fields. It gives an AI agent (or a quick CLI consumer)
// enough metadata to decide which detailed view to query next without
// echoing the whole Result. Time fields are integer milliseconds
// (matches schema v8).
type Overview struct {
	SchemaVersion int    `json:"schemaVersion"`
	FilePath      string `json:"filePath,omitempty"`
	// Map is the canonical map SHORTNAME (dm2, dm3, …) — Result.EffectiveMap
	// (demoinfo → serverinfo fallback), the same value searchGames rows,
	// serverinfo and Result.Match.Map carry, so a caller can join on it.
	Map string `json:"map,omitempty"`
	// MapTitle is the display-only level title (Result.Match.MapTitle, e.g.
	// "Claustrophobopolis" on dm2). Never an identifier. Omitted when it
	// equals Map — the common case where the map has no distinct title
	// (repo precedent: MessageClean elides when identical to the raw
	// message).
	MapTitle   string           `json:"mapTitle,omitempty"`
	GameDir    string           `json:"gameDir,omitempty"`
	Mode       string           `json:"mode,omitempty"`
	Matchtag   string           `json:"matchtag,omitempty"`
	Duration   int32            `json:"duration"`
	MatchStart int32            `json:"matchStart"`
	MatchEnd   int32            `json:"matchEnd"`
	Teams      []OverviewTeam   `json:"teams,omitempty"`
	Players    []OverviewPlayer `json:"players"`
	// Available is the per-demo capability manifest: which detailed views
	// this demo can actually answer. See OverviewAvailable — it is the
	// reason to call this endpoint first, and replaces both the ad-hoc
	// hasRegionControl flag and the inlined highlight lists that used to
	// live here (schema v70).
	Available OverviewAvailable `json:"available"`
	LocCount  int               `json:"locCount"`
	// Timing is the demo-open wall-clock anchor + pauses (from
	// streams.global). It lets a REST/MCP consumer map any match-relative
	// game time to real time without fetching streams. Omitted when the
	// demo carries no wall-clock source. See OverviewTiming.
	Timing *OverviewTiming `json:"timing,omitempty"`
	// PlayerUserIDs maps player name → hub.quakeworld.nu user id. Use it
	// to build deep links of the form
	// https://hub.quakeworld.nu/games/<gameId>?track=<userId>.
	//
	// A userid identifies one CONNECTION, so a player who reconnected has
	// held more than one. The id here is the last session of theirs that
	// had play — normally the one live at the end of the demo, and what a
	// `track=` resolves for a still-connected player; the ranking is by last
	// play evidence, so an exact tie in it resolves to the lower slot rather
	// than to the surviving connection (schema v66; before that it was
	// the first id seen on their wire slot, which after any handover or
	// rejoin belonged to somebody else). Timestamped surfaces resolve their
	// own: the timeline artifact's fragStreaks / powerupEvents /
	// demoMarkers each carry the id valid at that moment, and
	// /player-stats' per-row sessions[] carries every one of a player's
	// ids with the window it was live in — the lossless form of this map.
	PlayerUserIDs map[string]int `json:"playerUserIDs,omitempty"`
	// Errors carries the analyzer's non-fatal errors verbatim (a
	// sub-analyzer's Finalize failed but the pipeline continued). A
	// non-empty list means the result is degraded — some sections may
	// be missing or partial. Surfaced here so a consumer sees it on the
	// first call without parsing the full result. Omitted when empty.
	Errors []string `json:"errors,omitempty"`
	// ParseWarnings is the READER's census (result.parseWarnings): wire
	// data the MVD decoder could not read at all — unknown svc_* /
	// temp-entity / hidden-message types, payloads that failed to parse.
	// Distinct from Errors, which reports analyzer failures over events
	// that DID decode. Non-empty means this demo hit a protocol gap and
	// the views downstream of it may be thin. Omitted on a clean parse,
	// which is the normal case.
	ParseWarnings *result.ParseWarnings `json:"parseWarnings,omitempty"`
}

// OverviewTeam mirrors result.TeamStat.
type OverviewTeam struct {
	Name  string `json:"name"`
	Frags int    `json:"frags"`
}

// OverviewTiming exposes the wall-clock anchors (streams.global) so a
// REST/MCP consumer can map a match-relative game time g (ms) to real time:
//
//	wallClockMs = demoStartUnixMs + demoOffset + g + Σ pauses[i].durationMs (atMs <= g)
//	             (±demoStartAccuracyMs)
//
// matchStartUnixMs is the separate "when was this played" anchor (schema v72):
// it is what the wire date markers state, and it is present on ~95% of demos
// against ~25% for the server-clock sources behind demoStartUnixMs.
// matchStartConfidence grades it — see RESULT_SCHEMA.md's GlobalStream section.
//
// All fields omitempty; the block itself is omitted when no wall-clock source
// is present. Pauses reuses the result shape: {atMs, durationMs}.
type OverviewTiming struct {
	DemoOffset           int32                  `json:"demoOffset,omitempty"`
	DemoStartUnixMs      int64                  `json:"demoStartUnixMs,omitempty"`
	DemoStartAccuracyMs  int32                  `json:"demoStartAccuracyMs,omitempty"`
	DemoStartSource      string                 `json:"demoStartSource,omitempty"`
	MatchStartUnixMs     int64                  `json:"matchStartUnixMs,omitempty"`
	MatchStartAccuracyMs int32                  `json:"matchStartAccuracyMs,omitempty"`
	MatchStartSource     string                 `json:"matchStartSource,omitempty"`
	MatchStartConfidence string                 `json:"matchStartConfidence,omitempty"`
	MatchStartNote       string                 `json:"matchStartNote,omitempty"`
	MatchEndUnixMs       int64                  `json:"matchEndUnixMs,omitempty"`
	Pauses               []result.TimelinePause `json:"pauses,omitempty"`
}

// OverviewPlayer carries each player's identity + scoreboard line, taken
// from MatchResult.Players: Frags is the canonical net score, Kills/Deaths/
// Suicides the frag-log-corrected counts (0 when the demo had no frag log).
type OverviewPlayer struct {
	Name     string `json:"name"`
	Team     string `json:"team,omitempty"`
	Frags    int    `json:"frags"`
	Kills    int    `json:"kills"`
	Deaths   int    `json:"deaths"`
	Suicides int    `json:"suicides"`
}

// BuildOverview composes an Overview from a parsed *Result. All inputs
// are optional — missing sections produce empty Overview fields rather
// than errors.
func BuildOverview(r *result.Result) Overview {
	ov := Overview{
		SchemaVersion: result.CurrentSchemaVersion,
	}
	if r == nil {
		return ov
	}
	ov.SchemaVersion = r.SchemaVersion
	ov.FilePath = r.FilePath
	ov.Errors = r.Errors
	ov.ParseWarnings = r.ParseWarnings

	// map = the canonical shortname, mapTitle = the display-only level title.
	// Match publishes both (result/match.go), but EffectiveMap stays the
	// accessor for the shortname: it resolves independent of Match, so an
	// overview of a demo with no match section still names its map.
	ov.Map = r.EffectiveMap()

	if r.Match != nil {
		if ov.Map == "" {
			ov.Map = r.Match.Map
		}
		// Elide the title only on a case-only difference (demoinfo "aerowalk"
		// vs BSP LevelName "Aerowalk"): those are the same map, not a distinct
		// pretty title. A near-echo like "Bravado -" is deliberately NOT
		// elided — case-only is the one fixed class we collapse.
		if r.Match.MapTitle != "" && !strings.EqualFold(r.Match.MapTitle, ov.Map) {
			ov.MapTitle = r.Match.MapTitle
		}
		ov.GameDir = r.Match.GameDir
		ov.Duration = r.Match.Duration
		for _, p := range r.Match.Players {
			ov.Players = append(ov.Players, OverviewPlayer{
				Name: p.Name, Team: p.Team, Frags: p.Frags,
				Kills: p.Kills, Deaths: p.Deaths, Suicides: p.Suicides,
			})
		}
		for _, t := range r.Match.Teams {
			ov.Teams = append(ov.Teams, OverviewTeam{Name: t.Name, Frags: t.Frags})
		}
	}
	if r.Streams != nil {
		g := r.Streams.Global
		ov.MatchStart = g.MatchStart
		ov.MatchEnd = g.MatchEnd
		if g.DemoOffset != 0 || g.DemoStartUnixMs != 0 || g.MatchStartUnixMs != 0 || len(g.Pauses) > 0 {
			ov.Timing = &OverviewTiming{
				DemoOffset:           g.DemoOffset,
				DemoStartUnixMs:      g.DemoStartUnixMs,
				DemoStartAccuracyMs:  g.DemoStartAccuracyMs,
				DemoStartSource:      g.DemoStartSource,
				MatchStartUnixMs:     g.MatchStartUnixMs,
				MatchStartAccuracyMs: g.MatchStartAccuracyMs,
				MatchStartSource:     g.MatchStartSource,
				MatchStartConfidence: g.MatchStartConfidence,
				MatchStartNote:       g.MatchStartNote,
				MatchEndUnixMs:       g.MatchEndUnixMs,
				Pauses:               g.Pauses,
			}
		}
	}
	if r.Metadata != nil && r.Metadata.MatchSettings != nil {
		ov.Mode = r.Metadata.MatchSettings.Mode
		ov.Matchtag = r.Metadata.MatchSettings.Matchtag
	}
	if ov.Mode == "" && r.Match != nil {
		// The countdown centerprint above is a KTX-era signal; Match.Mode
		// resolves the demoinfo / //finalscores vocabulary instead, which is
		// what the pre-ktxstats half of the archive has (schema v72). It is a
		// different spelling of the same idea ("duel" vs "Duel") — hence the
		// fallback rather than a merge; result.MatchResult.Sources.Mode names
		// which one a demo got.
		ov.Mode = r.Match.Mode
	}
	if r.TimelineAnalysis != nil {
		ov.LocCount = len(r.TimelineAnalysis.LocTable)
		if len(r.TimelineAnalysis.PlayerUserIDs) > 0 {
			ov.PlayerUserIDs = r.TimelineAnalysis.PlayerUserIDs
		}
	}
	ov.Available = buildAvailability(r)

	// Stable ordering — players by frags desc, teams by frags desc.
	sort.SliceStable(ov.Players, func(i, j int) bool {
		return ov.Players[i].Frags > ov.Players[j].Frags
	})
	sort.SliceStable(ov.Teams, func(i, j int) bool {
		return ov.Teams[i].Frags > ov.Teams[j].Frags
	})

	return ov
}

// OverviewEnvelope wraps the /overview body with a fixed timeUnit echo (schema
// v57). Overview's descriptive times (duration, matchStart/End, streak/powerup
// start+duration) are all int32 milliseconds, so the echo is a constant "ms".
// Embedding flattens Overview's fields, so the body is Overview verbatim plus a
// leading timeUnit — no parallel field list to drift. The `timing` block keeps
// its own explicit *Ms names (demoOffset, pauses[].atMs/durationMs) — the
// wall-clock anchor island, like /demoinfo.
type OverviewEnvelope struct {
	TimeUnit view.TimeUnit `json:"timeUnit"`
	Overview
}

// OverviewAvailable is the per-demo capability manifest (schema v70): for
// each detailed view, whether THIS demo on THIS deployment can answer it.
//
// It exists because availability was previously undiscoverable. A consumer
// learned that a demo has no damage stream by calling /damage and reading a
// 422, and for the BSP-derived capabilities it could not learn at all: those
// depend on which BSPs the SERVER was provisioned with, so the same demo
// answers differently on two deployments and nothing in the response said
// so. That is precisely what an overview is for, and it is why this block
// replaced the inlined highlight lists — those were copies of data three
// other endpoints already served.
//
// Every eager flag mirrors the predicate behind that view's 422 (the
// `eagerArtifacts` table in artifacts.go), and TestOverviewAvailabilityCovers
// pins the two together so a new 422-able view cannot be added without a flag
// appearing here. That drift guard is the point: the old ad-hoc has* fields
// went stale precisely because nothing tied them to anything.
//
// A true flag means the section EXISTS, not that it is non-empty — a demo
// where nobody fired still reports shots: true with an empty shot list.
type OverviewAvailable struct {
	// The eager sections, each mirroring its 422 predicate.
	DemoInfo    bool `json:"demoInfo"`    // KTX demoinfo block (non-KTX / pre-match abort demos lack it)
	Metadata    bool `json:"metadata"`    // fullserverinfo + countdown centerprint
	Frags       bool `json:"frags"`       // obituary-derived frag log
	Damage      bool `json:"damage"`      // KTX mvdhidden_dmgdone stream
	Shots       bool `json:"shots"`       // decoded weapon fires
	Aim         bool `json:"aim"`         // needs shots + position/view streams
	LocGraph    bool `json:"locGraph"`    // needs a position track
	Opening     bool `json:"opening"`     // needs a detected match start
	PlayerStats bool `json:"playerStats"` // needs player streams (a missing KTX block is NOT a reason)

	// RegionControl is the former hasRegionControl, unchanged in meaning:
	// the timeline carries region-control output with at least one region.
	RegionControl bool `json:"regionControl"`

	// The BSP-derived trio — the ones a consumer cannot infer from anything
	// else, because they turn on server-side map provisioning rather than on
	// what the demo recorded.
	//
	// Height and Liquid report MEASUREDNESS, like every other flag here —
	// whether the column was computed at all, NOT whether it holds a
	// non-zero value. The distinction is the whole point: when the gate
	// opens the column is filled for every position sample, so a map with
	// no water yields an all-zero `lq` that is a genuine reading of "dry".
	// A flag keyed on non-zero-ness would collapse that into the same false
	// as an unprovisioned server, leaving a consumer unable to tell "this
	// map has no water" from "nobody looked".
	//
	// They are separate flags because they ride SEPARATE gates: the floor
	// trace needs the collision hull and the liquid probe needs the vis BSP
	// (analyzer/timeline_streams.go:946-951), and a map can provision one
	// without the other.
	Height bool `json:"height"` // pos.h — floor-height column computed (NoFloor where there is no floor)
	Liquid bool `json:"liquid"` // pos.lq — liquid column computed (0 where dry)

	// LOS covers BOTH the los and pvs interval sets: they come off one pass
	// (analyzer.ComputeLOS) behind one BSP gate, with PVS ⊇ LOS by
	// construction (result/streams.go), so two flags could never disagree
	// and a second one would only invite a consumer to think they might.
	//
	// Unlike the eager flags this is a PREDICTION, not a reading — the pass
	// is heavy and lazy, so running it to answer an overview would defeat
	// the point. It reports the cheap half of ComputeLOS's gate: streams,
	// at least two players, a map name, and a provisioned BSP. The residual
	// false positive is a provisioned BSP whose visibility data will not
	// load, which /los still answers with a 422.
	LOS bool `json:"los"`
}

// buildAvailability fills the manifest. Each eager flag calls the SAME view
// accessor its endpoint does, rather than re-testing the underlying field, so
// the manifest cannot disagree with the 422 it predicts — the accessors are
// nil-checks, so this stays as cheap as the rest of the overview.
func buildAvailability(r *result.Result) OverviewAvailable {
	ok := func(err error) bool { return err == nil }
	_, frags := view.Frags(r, view.FragOptions{})
	_, damage := view.Damage(r, view.DamageOptions{Dmg: "both"})
	_, shots := view.Shots(r)
	_, aim := view.Aim(r, view.AimOptions{})
	_, locGraph := view.LocGraph(r)
	_, playerStats := view.PlayerStats(r, view.PlayerStatsOptions{})
	_, demoInfo := view.DemoInfo(r)
	_, metadata := view.Metadata(r)

	a := OverviewAvailable{
		DemoInfo:    ok(demoInfo),
		Metadata:    ok(metadata),
		Frags:       ok(frags),
		Damage:      ok(damage),
		Shots:       ok(shots),
		Aim:         ok(aim),
		LocGraph:    ok(locGraph),
		Opening:     r.Opening != nil,
		PlayerStats: ok(playerStats),
	}
	if r.TimelineAnalysis != nil {
		a.RegionControl = r.TimelineAnalysis.RegionControl != nil &&
			len(r.TimelineAnalysis.RegionControl.Regions) > 0
	}
	a.Height, a.Liquid = bspDerivedMeasured(r)
	a.LOS = losPredicted(r)
	return a
}

// bspDerivedMeasured reports whether the BSP-derived position columns were
// COMPUTED, which is what the manifest promises — not whether they hold a
// non-zero value.
//
// Each column is allocated only when its gate opened and is then filled for
// every sample (timeline_streams.go:946-951), so length is the exact test:
// a dry map produces a full-length all-zero `lq`, which is a reading, while
// an unprovisioned server produces no column at all. An earlier version of
// this scanned for a non-zero entry and could not tell those apart — on
// aerowalk, a map with no water, it reported the same false as a server
// missing the BSP entirely.
func bspDerivedMeasured(r *result.Result) (height, liquid bool) {
	if r.Streams == nil {
		return false, false
	}
	for i := range r.Streams.Players {
		pos := r.Streams.Players[i].Position
		if pos == nil {
			continue
		}
		height = height || len(pos.H) > 0
		liquid = liquid || len(pos.Lq) > 0
		if height && liquid {
			return true, true
		}
	}
	return height, liquid
}

// losPredicted is the cheap half of analyzer.ComputeLOS's gate — see
// OverviewAvailable.LOS for why this is a prediction rather than a reading.
// When the pass has already run, its own verdict is used instead, which is
// exact.
func losPredicted(r *result.Result) bool {
	if r.Streams == nil || len(r.Streams.Players) < 2 {
		return false
	}
	if r.Streams.LOSComputed {
		for i := range r.Streams.Players {
			if len(r.Streams.Players[i].PVS) > 0 {
				return true
			}
		}
		return false
	}
	m := r.EffectiveMap()
	return m != "" && mapbsp.LoadBytes(m) != nil
}
