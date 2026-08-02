package main

import (
	"sort"
	"strings"

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
	MapTitle         string            `json:"mapTitle,omitempty"`
	GameDir          string            `json:"gameDir,omitempty"`
	Mode             string            `json:"mode,omitempty"`
	Matchtag         string            `json:"matchtag,omitempty"`
	Duration         int32             `json:"duration"`
	MatchStart       int32             `json:"matchStart"`
	MatchEnd         int32             `json:"matchEnd"`
	Teams            []OverviewTeam    `json:"teams,omitempty"`
	Players          []OverviewPlayer  `json:"players"`
	TopStreaks       []OverviewStreak  `json:"topStreaks,omitempty"`
	TopPowerups      []OverviewPowerup `json:"topPowerups,omitempty"`
	LocCount         int               `json:"locCount"`
	HasRegionControl bool              `json:"hasRegionControl"`
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
}

// OverviewTeam mirrors result.TeamStat.
type OverviewTeam struct {
	Name  string `json:"name"`
	Frags int    `json:"frags"`
}

// OverviewTiming exposes the demo-open wall-clock anchor (streams.global) so a
// REST/MCP consumer can map a match-relative game time g (ms) to real time:
//
//	wallClockMs = demoStartUnixMs + demoOffset + g + Σ pauses[i].durationMs (atMs <= g)
//	             (±demoStartAccuracyMs)
//
// All fields omitempty; the block itself is omitted when no wall-clock source
// is present. Pauses reuses the result shape: {atMs, durationMs}.
type OverviewTiming struct {
	DemoOffset          int32                  `json:"demoOffset,omitempty"`
	DemoStartUnixMs     int64                  `json:"demoStartUnixMs,omitempty"`
	DemoStartAccuracyMs int32                  `json:"demoStartAccuracyMs,omitempty"`
	Pauses              []result.TimelinePause `json:"pauses,omitempty"`
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

// OverviewStreak is a slimmed-down result.FragStreakEvent.
type OverviewStreak struct {
	Player   string `json:"player"`
	Team     string `json:"team,omitempty"`
	Weapon   string `json:"weapon,omitempty"`
	Length   int    `json:"length"`
	Start    int32  `json:"start"`    // ms
	Duration int32  `json:"duration"` // ms
}

// OverviewPowerup is a slimmed-down result.PowerupEvent.
type OverviewPowerup struct {
	Player   string `json:"player"`
	Team     string `json:"team,omitempty"`
	Type     string `json:"type"`
	Start    int32  `json:"start"`    // ms
	Duration int32  `json:"duration"` // ms
	Frags    int    `json:"frags"`
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
		if g.DemoOffset != 0 || g.DemoStartUnixMs != 0 || len(g.Pauses) > 0 {
			ov.Timing = &OverviewTiming{
				DemoOffset:          g.DemoOffset,
				DemoStartUnixMs:     g.DemoStartUnixMs,
				DemoStartAccuracyMs: g.DemoStartAccuracyMs,
				Pauses:              g.Pauses,
			}
		}
	}
	if r.Metadata != nil && r.Metadata.MatchSettings != nil {
		ov.Mode = r.Metadata.MatchSettings.Mode
		ov.Matchtag = r.Metadata.MatchSettings.Matchtag
	}
	if r.TimelineAnalysis != nil {
		ov.LocCount = len(r.TimelineAnalysis.LocTable)
		ov.HasRegionControl = r.TimelineAnalysis.RegionControl != nil &&
			len(r.TimelineAnalysis.RegionControl.Regions) > 0

		ov.TopStreaks = topStreaks(r.TimelineAnalysis.FragStreaks, 5)
		ov.TopPowerups = topPowerups(r.TimelineAnalysis.PowerupEvents, 5)

		if len(r.TimelineAnalysis.PlayerUserIDs) > 0 {
			ov.PlayerUserIDs = r.TimelineAnalysis.PlayerUserIDs
		}
	}

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

func topStreaks(in []result.FragStreakEvent, n int) []OverviewStreak {
	if len(in) == 0 {
		return nil
	}
	out := make([]OverviewStreak, 0, len(in))
	for _, s := range in {
		out = append(out, OverviewStreak{
			Player:   s.PlayerName,
			Team:     s.Team,
			Weapon:   s.Ewep,
			Length:   s.Frags,
			Start:    s.Time,
			Duration: s.Duration,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Length > out[j].Length })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func topPowerups(in []result.PowerupEvent, n int) []OverviewPowerup {
	if len(in) == 0 {
		return nil
	}
	out := make([]OverviewPowerup, 0, len(in))
	for _, p := range in {
		out = append(out, OverviewPowerup{
			Player:   p.PlayerName,
			Team:     p.Team,
			Type:     p.PowerupType,
			Start:    p.Time,
			Duration: p.Duration,
			Frags:    p.Frags,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Frags > out[j].Frags })
	if len(out) > n {
		out = out[:n]
	}
	return out
}
