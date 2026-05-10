//go:build js && wasm

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"syscall/js"

	"github.com/mvd-analyzer/qwanalytics/analyzer"
	"github.com/mvd-analyzer/qwanalytics/config"
	"github.com/mvd-analyzer/qwanalytics/result"
	"github.com/mvd-analyzer/qwanalytics/view"
	"github.com/mvd-analyzer/qwdemo/mvdfile"
)

// lastResult retains the most recently analysed demo so JS can call
// recomputeRegionControl with edited regions and get fresh stats
// without re-parsing the demo. Cleared/replaced by each analyze call.
var lastResult *analyzer.Result

func analyze(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return errorJSON("missing data argument")
	}

	filename := "demo.mvd"
	if len(args) >= 2 {
		filename = args[1].String()
	}

	// Copy Uint8Array from JS to Go
	jsData := args[0]
	length := jsData.Get("length").Int()
	data := make([]byte, length)
	js.CopyBytesToGo(data, jsData)

	// Handle gzip decompression
	reader, err := mvdfile.NewReader(bytes.NewReader(data))
	if err != nil {
		return errorJSON(err.Error())
	}
	defer reader.Close()

	// Run analysis pipeline
	registry := analyzer.NewDefaultRegistry()
	res, err := registry.AnalyzeReader(reader, filename)
	if err != nil {
		return errorJSON(err.Error())
	}

	lastResult = res

	jsonBytes, err := json.Marshal(res)
	if err != nil {
		return errorJSON(err.Error())
	}

	return string(jsonBytes)
}

// getDefaultBuckets returns the v6-shape []HighResBucket array — what
// the existing qw-web frontend's panels iterate. Internally this is
// view.Buckets({windowMs:50, fields:all, reducers:legacy,
// includeTeam:true}) followed by view.ToLegacyHighResBuckets.
//
// Phase 1.5 of the plan migrates panels to call view.Buckets directly
// via getBuckets; this shim is the bridge that keeps the existing
// frontend untouched.
func getDefaultBuckets(this js.Value, args []js.Value) interface{} {
	if lastResult == nil {
		return errorJSON("no demo analyzed yet")
	}
	bv, err := view.Buckets(lastResult, view.BucketsOptions{
		WindowMs:    50,
		Fields:      view.AllStandardFields,
		Reducers:    view.LegacyReducerSet,
		IncludeTeam: true,
	})
	if err != nil {
		return errorJSON(err.Error())
	}
	legacy := view.ToLegacyHighResBuckets(bv)
	b, err := json.Marshal(legacy)
	if err != nil {
		return errorJSON(err.Error())
	}
	return string(b)
}

// getBuckets is the new query API surface. Argument is a JSON string
// of view.BucketsOptions. Returns BucketsView JSON.
func getBuckets(this js.Value, args []js.Value) interface{} {
	if lastResult == nil {
		return errorJSON("no demo analyzed yet")
	}
	if len(args) < 1 {
		return errorJSON("missing options argument")
	}
	var opts view.BucketsOptions
	if err := json.Unmarshal([]byte(args[0].String()), &opts); err != nil {
		return errorJSON("bad options JSON: " + err.Error())
	}
	bv, err := view.Buckets(lastResult, opts)
	if err != nil {
		return errorJSON(err.Error())
	}
	b, err := json.Marshal(bv)
	if err != nil {
		return errorJSON(err.Error())
	}
	return string(b)
}

// getEvents returns a tagged event list. Argument is a JSON string of
// view.EventsFilter. Returns EventsView JSON.
func getEvents(this js.Value, args []js.Value) interface{} {
	if lastResult == nil {
		return errorJSON("no demo analyzed yet")
	}
	if len(args) < 1 {
		return errorJSON("missing filter argument")
	}
	var filter view.EventsFilter
	if err := json.Unmarshal([]byte(args[0].String()), &filter); err != nil {
		return errorJSON("bad filter JSON: " + err.Error())
	}
	v, err := view.Events(lastResult, filter)
	if err != nil {
		return errorJSON(err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		return errorJSON(err.Error())
	}
	return string(b)
}

// getStreamSlice returns raw change entries in a window — right shape
// for AI agents inspecting a short event.
func getStreamSlice(this js.Value, args []js.Value) interface{} {
	if lastResult == nil {
		return errorJSON("no demo analyzed yet")
	}
	if len(args) < 1 {
		return errorJSON("missing options argument")
	}
	var opts view.StreamSliceOptions
	if err := json.Unmarshal([]byte(args[0].String()), &opts); err != nil {
		return errorJSON("bad options JSON: " + err.Error())
	}
	v, err := view.StreamSlice(lastResult, opts)
	if err != nil {
		return errorJSON(err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		return errorJSON(err.Error())
	}
	return string(b)
}

// getStateAt resolves each requested field's value at a specific time.
func getStateAt(this js.Value, args []js.Value) interface{} {
	if lastResult == nil {
		return errorJSON("no demo analyzed yet")
	}
	if len(args) < 1 {
		return errorJSON("missing options argument")
	}
	var opts view.StateAtOptions
	if err := json.Unmarshal([]byte(args[0].String()), &opts); err != nil {
		return errorJSON("bad options JSON: " + err.Error())
	}
	v, err := view.StateAt(lastResult, opts)
	if err != nil {
		return errorJSON(err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		return errorJSON(err.Error())
	}
	return string(b)
}

// getLocTrails returns per-player loc residences.
func getLocTrails(this js.Value, args []js.Value) interface{} {
	if lastResult == nil {
		return errorJSON("no demo analyzed yet")
	}
	var opts view.LocTrailsOptions
	if len(args) >= 1 && args[0].String() != "" {
		if err := json.Unmarshal([]byte(args[0].String()), &opts); err != nil {
			return errorJSON("bad options JSON: " + err.Error())
		}
	}
	v, err := view.LocTrails(lastResult, opts)
	if err != nil {
		return errorJSON(err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		return errorJSON(err.Error())
	}
	return string(b)
}

// recomputeRegionControl is the JS-callable region recompute hook.
// At schema v7 it derives buckets via view.Buckets internally
// (replacing the old read of ta.HighResBuckets) before classifying.
//
// The caller passes a JSON string of {"regions":[{"name":...,"locs":[...]}]}.
// Returns an error envelope when no demo has been analysed yet, the
// JSON is malformed, or the cached match has no two-team layout.
func recomputeRegionControl(this js.Value, args []js.Value) interface{} {
	if lastResult == nil || lastResult.TimelineAnalysis == nil {
		return errorJSON("no demo analyzed yet")
	}
	if len(args) < 1 {
		return errorJSON("missing regions argument")
	}
	var ov config.MapRegionOverrides
	if err := json.Unmarshal([]byte(args[0].String()), &ov); err != nil {
		return errorJSON("bad regions JSON: " + err.Error())
	}

	ta := lastResult.TimelineAnalysis
	if ta.RegionControl == nil || ta.RegionControl.TeamA == "" || ta.RegionControl.TeamB == "" {
		return errorJSON("region control unavailable (non-binary team layout)")
	}

	nameToTeam := buildNameToTeam(lastResult)
	regions := make([]result.ControlRegion, 0, len(ov.Regions))
	for _, r := range ov.Regions {
		regions = append(regions, result.ControlRegion{
			Name: r.Name,
			Locs: append([]string(nil), r.Locs...),
		})
	}
	teamOf := func(name string) string {
		// Strip the "#slot" disambiguation suffix (D12) if present so
		// regions classifier sees the canonical name.
		base := name
		if idx := strings.LastIndex(name, "#"); idx >= 0 {
			base = name[:idx]
		}
		return nameToTeam[base]
	}

	rcv, err := view.RegionControl(
		lastResult, regions,
		ta.RegionControl.TeamA, ta.RegionControl.TeamB,
		teamOf, analyzer.ComputeRegionControl,
		view.RegionControlOptions{WindowMs: 50},
	)
	if err != nil {
		return errorJSON(err.Error())
	}

	b, err := json.Marshal(rcv)
	if err != nil {
		return errorJSON(err.Error())
	}
	return string(b)
}

// buildNameToTeam derives a player-name -> team mapping from the
// analysed result. Prefers Match.Players (the canonical scoreboard
// view); falls back to demoinfo if Match is absent.
func buildNameToTeam(r *analyzer.Result) map[string]string {
	m := make(map[string]string)
	if r.Match != nil {
		for _, p := range r.Match.Players {
			if p.Name != "" && p.Team != "" {
				m[p.Name] = p.Team
			}
		}
	}
	if r.DemoInfo != nil {
		for _, p := range r.DemoInfo.Players {
			if p.Name != "" && p.Team != "" {
				if _, ok := m[p.Name]; !ok {
					m[p.Name] = p.Team
				}
			}
		}
	}
	return m
}

func errorJSON(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

// Set at build time via -ldflags.
var (
	GitHash   = "dev"
	GitTag    = "dev"
	BuildDate = "unknown"
)

func main() {
	js.Global().Set("analyzeMVD", js.FuncOf(analyze))
	js.Global().Set("recomputeRegionControl", js.FuncOf(recomputeRegionControl))
	js.Global().Set("getDefaultBuckets", js.FuncOf(getDefaultBuckets))
	js.Global().Set("getBuckets", js.FuncOf(getBuckets))
	js.Global().Set("getEvents", js.FuncOf(getEvents))
	js.Global().Set("getStreamSlice", js.FuncOf(getStreamSlice))
	js.Global().Set("getStateAt", js.FuncOf(getStateAt))
	js.Global().Set("getLocTrails", js.FuncOf(getLocTrails))
	js.Global().Set("wasmVersion", map[string]interface{}{
		"hash": GitHash,
		"tag":  GitTag,
		"date": BuildDate,
	})
	// Block forever to keep WASM instance alive
	select {}
}
