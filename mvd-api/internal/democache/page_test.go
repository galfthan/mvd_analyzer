package democache

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// TestPageResult_CachedWithoutLOS: the /result body is built once per demo
// and kept, carries no line of sight even after /los spliced it into the
// stored Result, and a raycast running beside the first build does not race
// its copy (run with -race). RemoveDemo drops the kept body.
func TestPageResult_CachedWithoutLOS(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	content := "page-result-demo"
	sha := sha256Hex([]byte(content))
	hub.addGame(4242, sha, content)
	id := DemoID{Kind: "gameId", GameID: 4242}

	c := New(t.TempDir(), hub.hubClient())
	c.Parse = streamsWithPlayers
	c.BuildLOS = func(_ *analyzer.LazyArtifact, res *result.Result) error {
		for i := range res.Streams.Players {
			res.Streams.Players[i].LOS = []result.LosTrack{{Other: 1}}
		}
		res.Streams.LOSComputed = true
		return nil
	}

	var wg sync.WaitGroup
	var first []byte
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, _, err := c.EnsureLOS(context.Background(), id); err != nil {
			t.Errorf("EnsureLOS: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		b, _, err := c.PageResult(context.Background(), id)
		if err != nil {
			t.Errorf("PageResult: %v", err)
		}
		first = b
	}()
	wg.Wait()

	zr, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"schemaVersion"`)) || bytes.Contains(body, []byte(`"los"`)) {
		t.Errorf("body: %.200s", body)
	}

	again, _, err := c.PageResult(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if &again[0] != &first[0] {
		t.Error("the second request rebuilt the body instead of reusing it")
	}
	c.RemoveDemo(sha)
	if c.pages.get(sha) != nil {
		t.Error("RemoveDemo left the kept body")
	}
}
