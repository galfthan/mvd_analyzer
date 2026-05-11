package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/mvd-analyzer/qwanalytics/analyzer"
	"github.com/mvd-analyzer/qwanalytics/internal/democache"
	"github.com/mvd-analyzer/qwanalytics/result"
	"github.com/mvd-analyzer/qwanalytics/view"
)

// localBackend implements MCPBackend by calling democache + view
// directly, without an HTTP hop. Used when `qw-mvd mcp` is invoked
// without -api URL.
type localBackend struct {
	store demoStore
}

// resolveDemoID parses the DemoID string used in tool inputs.
func resolveDemoID(s string) (democache.DemoID, error) {
	if s == "" {
		return democache.DemoID{}, errors.New("demoId is required (use loadDemo to get one)")
	}
	return democache.ParseDemoID(s)
}

func (b *localBackend) LoadDemo(ctx context.Context, in LoadDemoInput) (*LoadDemoOutput, error) {
	var id democache.DemoID
	switch {
	case in.GameID > 0 && in.SHA256 == "":
		id = democache.DemoID{Kind: "gameId", GameID: in.GameID}
	case in.SHA256 != "" && in.GameID == 0:
		parsed, err := democache.ParseDemoID("sha:" + in.SHA256)
		if err != nil {
			return nil, err
		}
		id = parsed
	default:
		return nil, errors.New("exactly one of gameId or sha256 must be set")
	}
	_, meta, err := b.store.GetResult(ctx, id)
	if err != nil {
		return nil, err
	}
	return &LoadDemoOutput{
		DemoID:        "sha:" + meta.SHA256,
		SHA256:        meta.SHA256,
		FromCache:     meta.FromCache,
		SchemaVersion: meta.SchemaVersion,
	}, nil
}

// resolve fetches the *Result for a tool input's DemoID.
func (b *localBackend) resolve(ctx context.Context, demoID string) (*result.Result, error) {
	id, err := resolveDemoID(demoID)
	if err != nil {
		return nil, err
	}
	r, _, err := b.store.GetResult(ctx, id)
	return r, err
}

func (b *localBackend) GetOverview(ctx context.Context, in GetOverviewInput) (*Overview, error) {
	r, err := b.resolve(ctx, in.DemoID)
	if err != nil {
		return nil, err
	}
	ov := BuildOverview(r)
	return &ov, nil
}

func (b *localBackend) GetBuckets(ctx context.Context, in GetBucketsInput) (*view.BucketsView, error) {
	r, err := b.resolve(ctx, in.DemoID)
	if err != nil {
		return nil, err
	}
	return view.Buckets(r, view.BucketsOptions{
		WindowMs:    in.WindowMs,
		StartTime:   in.StartTime,
		EndTime:     in.EndTime,
		Players:     in.Players,
		Fields:      in.Fields,
		Reducers:    in.Reducers,
		IncludeTeam: in.IncludeTeam,
	})
}

func (b *localBackend) GetEvents(ctx context.Context, in GetEventsInput) (*view.EventsView, error) {
	r, err := b.resolve(ctx, in.DemoID)
	if err != nil {
		return nil, err
	}
	return view.Events(r, view.EventsFilter{
		StartTime: in.StartTime,
		EndTime:   in.EndTime,
		Players:   in.Players,
		Types:     in.Types,
	})
}

func (b *localBackend) GetStreamSlice(ctx context.Context, in GetStreamSliceInput) (*view.StreamSliceView, error) {
	r, err := b.resolve(ctx, in.DemoID)
	if err != nil {
		return nil, err
	}
	return view.StreamSlice(r, view.StreamSliceOptions{
		StartTime: in.StartTime,
		EndTime:   in.EndTime,
		Players:   in.Players,
		Fields:    in.Fields,
	})
}

func (b *localBackend) GetStateAt(ctx context.Context, in GetStateAtInput) (*view.StateAtView, error) {
	r, err := b.resolve(ctx, in.DemoID)
	if err != nil {
		return nil, err
	}
	return view.StateAt(r, view.StateAtOptions{
		Time:    in.Time,
		Players: in.Players,
		Fields:  in.Fields,
	})
}

func (b *localBackend) GetLocTrails(ctx context.Context, in GetLocTrailsInput) (*view.LocTrailsView, error) {
	r, err := b.resolve(ctx, in.DemoID)
	if err != nil {
		return nil, err
	}
	return view.LocTrails(r, view.LocTrailsOptions{
		Players:    in.Players,
		MinDwellMs: in.MinDwellMs,
		StartTime:  in.StartTime,
		EndTime:    in.EndTime,
	})
}

func (b *localBackend) GetRegionControl(ctx context.Context, in GetRegionControlInput) (*result.RegionControlResult, error) {
	r, err := b.resolve(ctx, in.DemoID)
	if err != nil {
		return nil, err
	}
	if r.TimelineAnalysis == nil || r.TimelineAnalysis.RegionControl == nil {
		return nil, fmt.Errorf("region-control unavailable: this demo has no region-control layout")
	}
	rc := r.TimelineAnalysis.RegionControl
	teamOf := func(name string) string {
		if r.Match == nil {
			return ""
		}
		for _, p := range r.Match.Players {
			if p.Name == name {
				return p.Team
			}
		}
		return ""
	}
	return view.RegionControl(
		r, rc.Regions, rc.TeamA, rc.TeamB,
		teamOf, analyzer.ComputeRegionControl,
		view.RegionControlOptions{WindowMs: in.WindowMs},
	)
}
