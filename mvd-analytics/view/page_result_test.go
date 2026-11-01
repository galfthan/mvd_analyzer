package view

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// TestPageResult: the page's Result carries the KTX-overlaid playerStats and
// no position tracks and no line of sight (a lazy pass mvd-api splices into
// its stored Result), and leaves the stored Result as the analyzer made it.
func TestPageResult(t *testing.T) {
	r := storedResult(true)
	r.Streams = gapFixture().Streams
	r.Streams.Players[0].LOS = []result.LosTrack{{Other: 1}}
	r.Streams.Players[0].PVS = []result.LosTrack{{Other: 1}}
	stored := r.PlayerStats
	before, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	p := PageResult(r)
	if want := mustPlayerStats(t, r, PlayerStatsOptions{}); !reflect.DeepEqual(p.PlayerStats, want) {
		t.Error("playerStats is not the KTX-overlaid section")
	}
	if p.PlayerStats == stored {
		t.Error("playerStats is the stored, derived section")
	}
	for i, pl := range p.Streams.Players {
		if pl.Position != nil {
			t.Errorf("player %d kept its position track", i)
		}
		if pl.LOS != nil || pl.PVS != nil {
			t.Errorf("player %d kept its line of sight", i)
		}
		if pl.Name != r.Streams.Players[i].Name {
			t.Errorf("player %d: %q, want %q", i, pl.Name, r.Streams.Players[i].Name)
		}
	}
	after, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("PageResult modified the stored Result")
	}

	// No streams at all (a parse that produced none): no panic, none added.
	if got := PageResult(&result.Result{PlayerStats: stored}); got.Streams != nil {
		t.Errorf("bare Result gained streams: %+v", got.Streams)
	}
}
