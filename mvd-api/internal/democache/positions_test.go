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
// keeps it in tier 3; a later process serves that file without parsing
// the demo (its parser fails here) and without a Result in memory.
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
	onDisk, err := os.ReadFile(positionPackPath(root, testSHA))
	if err != nil || !bytes.Equal(onDisk, data) {
		t.Fatalf("tier-3 pack not written as served (%v)", err)
	}

	// Without tiers 1 and 2, a GetResult would have to download and parse.
	_ = os.Remove(resultPath(root, result.CurrentSchemaVersion, testSHA))
	_ = os.Remove(mvdPath(root, testSHA))
	fresh := New(root, hub.hubClient())
	fresh.Parse = func(context.Context, []byte, string) (*result.Result, error) {
		t.Fatal("a pack on disk must not need the Result")
		return nil, nil
	}
	again, meta, err := fresh.PositionPack(ctx, id)
	if err != nil || !bytes.Equal(again, data) || !meta.FromCache || meta.SHA256 != testSHA {
		t.Fatalf("fresh process: %v, %d bytes, %+v", err, len(again), meta)
	}
}

// TestPositionPack_CorruptFileRebuilt: a tier-3 pack that fails its check
// (a valid gzip of the wrong bytes here) is discarded and rebuilt, not
// served forever.
func TestPositionPack_CorruptFileRebuilt(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)
	c, root := newTestCache(t, hub.hubClient(), &stubParser{})
	c.Parse = trackedStreams
	ctx := context.Background()
	id := DemoID{Kind: "gameId", GameID: 42}
	good, _, err := c.PositionPack(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	for name, bad := range map[string][]byte{
		"not gzip":  []byte("sentinel"),
		"truncated": good[:len(good)/2],
		"not a pack": func() []byte {
			var b bytes.Buffer
			zw := gzip.NewWriter(&b)
			_, _ = zw.Write([]byte("garbage bytes"))
			_ = zw.Close()
			return b.Bytes()
		}(),
	} {
		if err := os.WriteFile(positionPackPath(root, testSHA), bad, 0o644); err != nil {
			t.Fatal(err)
		}
		fresh := New(root, hub.hubClient())
		fresh.Parse = trackedStreams
		got, _, err := fresh.PositionPack(ctx, id)
		if err != nil || !bytes.Equal(got, good) {
			t.Errorf("%s: served %d bytes (%v), want the rebuilt pack", name, len(got), err)
		}
	}
}
