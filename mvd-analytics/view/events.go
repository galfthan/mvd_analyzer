package view

import (
	"sort"

	"github.com/mvd-analyzer/qwanalytics/result"
)

// EventsFilter narrows an Events query by time, player set, or event
// type. An empty Types filter selects the discrete-event set (D15);
// an explicit list — even with one entry — overrides that default.
type EventsFilter struct {
	StartTime float64
	EndTime   float64
	Players   []string
	Types     []string
}

// EventsView is the response shape: a flat list of TaggedEvent in
// time order.
type EventsView struct {
	Events []TaggedEvent `json:"events"`
}

// TaggedEvent is a uniform time-ordered event record. Detail is
// always non-nil for the types that have details (frag, weapon, …)
// and may be nil for spawn / death where the timestamp is the whole
// signal.
type TaggedEvent struct {
	T      float64        `json:"t"`
	Type   string         `json:"type"`
	Player string         `json:"player,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

// Default Types when EventsFilter.Types is empty (D15: omit the
// high-frequency change events that drown the discrete-event story).
var defaultEventTypes = []string{
	"frag", "powerup", "streak", "spawn", "death", "weapon", "item", "chat",
}

// Events returns a time-ordered list of events matching the filter.
// Synthesised from result.TimelineAnalysis.{FragEvents, PowerupEvents,
// FragStreaks}, result.Messages, and result.Streams change entries.
func Events(r *result.Result, filter EventsFilter) (*EventsView, error) {
	if r == nil {
		return &EventsView{}, nil
	}
	types := filter.Types
	if len(types) == 0 {
		types = defaultEventTypes
	}
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[t] = true
	}
	pf := newPlayerFilter(filter.Players)
	end := filter.EndTime
	if end == 0 && r.Streams != nil {
		end = r.Streams.Global.MatchEnd
	}
	if end == 0 {
		end = inferMatchEnd(r)
	}

	var events []TaggedEvent
	if want["frag"] && r.TimelineAnalysis != nil {
		for _, fe := range r.TimelineAnalysis.FragEvents {
			if !inWindow(fe.Time, filter.StartTime, end) {
				continue
			}
			if !pf.accepts(fe.Player) {
				continue
			}
			detail := map[string]any{"team": fe.Team, "delta": fe.Delta}
			events = append(events, TaggedEvent{
				T: fe.Time, Type: "frag", Player: fe.Player, Detail: detail,
			})
		}
	}
	if want["powerup"] && r.TimelineAnalysis != nil {
		for _, pe := range r.TimelineAnalysis.PowerupEvents {
			if !inWindow(pe.Time, filter.StartTime, end) {
				continue
			}
			if !pf.accepts(pe.PlayerName) {
				continue
			}
			detail := map[string]any{
				"powerup":  pe.PowerupType,
				"endTime":  pe.EndTime,
				"duration": pe.Duration,
				"frags":    pe.Frags,
				"team":     pe.Team,
			}
			events = append(events, TaggedEvent{
				T: pe.Time, Type: "powerup", Player: pe.PlayerName, Detail: detail,
			})
		}
	}
	if want["streak"] && r.TimelineAnalysis != nil {
		for _, fs := range r.TimelineAnalysis.FragStreaks {
			if !inWindow(fs.Time, filter.StartTime, end) {
				continue
			}
			if !pf.accepts(fs.PlayerName) {
				continue
			}
			detail := map[string]any{
				"length":   fs.Frags,
				"endTime":  fs.EndTime,
				"duration": fs.Duration,
				"weapon":   fs.Ewep,
				"team":     fs.Team,
			}
			events = append(events, TaggedEvent{
				T: fs.Time, Type: "streak", Player: fs.PlayerName, Detail: detail,
			})
		}
	}
	if want["chat"] && r.Messages != nil {
		for _, msg := range r.Messages.Events {
			if !inWindow(msg.Time, filter.StartTime, end) {
				continue
			}
			if !pf.accepts(msg.Player) {
				continue
			}
			detail := map[string]any{"text": msg.Message}
			if msg.MessageClean != "" {
				detail["clean"] = msg.MessageClean
			}
			if msg.Team != "" {
				detail["team"] = msg.Team
			}
			events = append(events, TaggedEvent{
				T: msg.Time, Type: "chat", Player: msg.Player, Detail: detail,
			})
		}
	}

	if r.Streams != nil {
		for _, p := range r.Streams.Players {
			if !pf.accepts(p.Name) {
				continue
			}
			if want["spawn"] {
				for _, t := range p.Spawns {
					if !inWindow(t, filter.StartTime, end) {
						continue
					}
					events = append(events, TaggedEvent{T: t, Type: "spawn", Player: p.Name})
				}
			}
			if want["death"] {
				for _, t := range p.Deaths {
					if !inWindow(t, filter.StartTime, end) {
						continue
					}
					events = append(events, TaggedEvent{T: t, Type: "death", Player: p.Name})
				}
			}
			if want["weapon"] {
				weaponIntervals := map[string][]result.Interval{
					"rl":  p.RL,
					"lg":  p.LG,
					"gl":  p.GL,
					"ssg": p.SSG,
					"sng": p.SNG,
				}
				events = appendIntervalEvents(events, p.Name, "weapon", weaponIntervals, filter.StartTime, end)
			}
			if want["item"] {
				powerupIntervals := map[string][]result.Interval{
					"q":  p.Quad,
					"pe": p.Pent,
					"r":  p.Ring,
				}
				events = appendIntervalEvents(events, p.Name, "item", powerupIntervals, filter.StartTime, end)
			}
			if want["health"] {
				prev := int16(0)
				for i, c := range p.Health {
					if !inWindow(c.T, filter.StartTime, end) {
						continue
					}
					detail := map[string]any{"value": c.V}
					if i > 0 {
						detail["delta"] = int(c.V) - int(prev)
					}
					events = append(events, TaggedEvent{T: c.T, Type: "health", Player: p.Name, Detail: detail})
					prev = c.V
				}
			}
			if want["armor"] {
				prev := int16(0)
				for i, c := range p.Armor {
					if !inWindow(c.T, filter.StartTime, end) {
						continue
					}
					detail := map[string]any{"value": c.V}
					if i > 0 {
						detail["delta"] = int(c.V) - int(prev)
					}
					events = append(events, TaggedEvent{T: c.T, Type: "armor", Player: p.Name, Detail: detail})
					prev = c.V
				}
			}
			if want["loc"] && r.TimelineAnalysis != nil {
				locTable := r.TimelineAnalysis.LocTable
				for _, c := range p.Loc {
					if !inWindow(c.T, filter.StartTime, end) {
						continue
					}
					locName := ""
					if int(c.V) >= 0 && int(c.V) < len(locTable) {
						locName = locTable[c.V]
					}
					events = append(events, TaggedEvent{
						T: c.T, Type: "loc", Player: p.Name,
						Detail: map[string]any{"loc": locName, "index": int(c.V)},
					})
				}
			}
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].T != events[j].T {
			return events[i].T < events[j].T
		}
		return events[i].Type < events[j].Type
	})
	return &EventsView{Events: events}, nil
}

func inWindow(t, start, end float64) bool {
	if start != 0 && t < start {
		return false
	}
	if end != 0 && t > end {
		return false
	}
	return true
}

func appendIntervalEvents(
	events []TaggedEvent,
	player, kindLabel string,
	streams map[string][]result.Interval,
	start, end float64,
) []TaggedEvent {
	for code, ivs := range streams {
		for _, iv := range ivs {
			if inWindow(iv.Start, start, end) {
				events = append(events, TaggedEvent{
					T: iv.Start, Type: kindLabel, Player: player,
					Detail: map[string]any{kindLabel: code, "kind": "gain"},
				})
			}
			if inWindow(iv.End, start, end) {
				events = append(events, TaggedEvent{
					T: iv.End, Type: kindLabel, Player: player,
					Detail: map[string]any{kindLabel: code, "kind": "lose"},
				})
			}
		}
	}
	return events
}

// inferMatchEnd is a fallback when r.Streams is absent. Reads
// Match.EndTime if present.
func inferMatchEnd(r *result.Result) float64 {
	if r.Match != nil {
		return r.Match.EndTime
	}
	return 0
}
