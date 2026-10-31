package democache

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func trackedStreams(_ context.Context, _ []byte, filename string) (*result.Result, error) {
	return &result.Result{
		SchemaVersion: result.CurrentSchemaVersion,
		FilePath:      filename,
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "A", Position: &result.PositionTrack{T: []int32{0, 13}, X: []float32{1, 2}, Y: []float32{3, 4}, Z: []float32{5, 6}}},
			{Name: "B"},
		}},
	}, nil
}

// TestPositionPack_BuiltOnceThenRead: the first request gzips the pack and
// keeps it in tier 3; the next one serves that file rather than
// rebuilding — proven by a sentinel only the file could hold.
func TestPositionPack_BuiltOnceThenRead(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)
	c, root := newTestCache(t, hub.hubClient(), &stubParser{})
	c.Parse = trackedStreams
	ctx := context.Background()
	id := DemoID{Kind: "gameId", GameID: 42}

	data, meta, err := c.PositionPack(ctx, id)
	if err != nil {
		t.Fatalf("PositionPack: %v", err)
	}
	if meta.SHA256 != testSHA {
		t.Fatalf("meta sha %q", meta.SHA256)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	res, _ := trackedStreams(ctx, nil, "")
	if !bytes.Equal(raw, result.EncodePositionPack(res.Streams)) {
		t.Fatal("served pack is not the Result's")
	}
	path := positionPackPath(root, testSHA)
	onDisk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(onDisk, data) {
		t.Fatalf("tier-3 pack not written as served (%v)", err)
	}

	if err := os.WriteFile(path, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, _, err := c.PositionPack(ctx, id)
	if err != nil || string(again) != "sentinel" {
		t.Fatalf("second request rebuilt instead of reading tier 3: %q, %v", again, err)
	}
}
