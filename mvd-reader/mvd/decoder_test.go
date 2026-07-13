package mvd

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// craftedBlock builds a minimal MVD stream: 1-byte time delta, 1-byte type,
// then a little-endian uint32 declared block size (no payload bytes follow).
func craftedBlock(msgType byte, declaredSize uint32) []byte {
	var b bytes.Buffer
	b.WriteByte(0x00)    // time delta
	b.WriteByte(msgType) // message type byte (player number 0)
	if msgType == DemMultiple {
		var mask [4]byte
		b.Write(mask[:]) // dem_multiple player mask
	}
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], declaredSize)
	b.Write(sz[:])
	return b.Bytes()
}

// TestOversizeBlockSizeRejected verifies that a crafted block-size header far
// beyond maxBlockSize yields a decode error instead of a multi-GiB allocation.
func TestOversizeBlockSizeRejected(t *testing.T) {
	cases := []struct {
		name    string
		msgType byte
	}{
		{"dem_multiple", DemMultiple},
		{"dem_single", DemSingle},
		{"dem_stats", DemStats},
		{"dem_all", DemAll},
		{"dem_read", DemRead},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ~4 GiB declared; ReadBytes would OOM if the cap were absent.
			stream := craftedBlock(tc.msgType, 0xFFFFFFFF)
			dec := NewDecoder(bytes.NewReader(stream))
			_, err := dec.NextMessage()
			if err == nil {
				t.Fatalf("expected decode error for oversize block, got nil")
			}
			if !strings.Contains(err.Error(), "exceeds maximum") {
				t.Fatalf("expected size-cap error, got %v", err)
			}
		})
	}
}

// TestAtCapBlockSizeAllocatesAndReadsToEOF verifies the cap is a ceiling, not a
// floor: a block declaring exactly maxBlockSize is accepted (it fails only on
// the truncated payload with EOF, not on the size check).
func TestAtCapBlockSizeAccepted(t *testing.T) {
	stream := craftedBlock(DemSingle, maxBlockSize)
	dec := NewDecoder(bytes.NewReader(stream))
	_, err := dec.NextMessage()
	if err == nil {
		t.Fatalf("expected truncated-payload error, got nil")
	}
	if strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("block at exactly the cap must not be rejected by the size check: %v", err)
	}
}

// TestTruncatedBlockPayloadUnchanged verifies a legitimately-sized block whose
// payload is truncated still surfaces the underlying read error (EOF), i.e. the
// cap does not alter behaviour on honest-but-truncated streams.
func TestTruncatedBlockPayloadUnchanged(t *testing.T) {
	stream := craftedBlock(DemAll, 16) // declares 16 payload bytes, supplies none
	dec := NewDecoder(bytes.NewReader(stream))
	_, err := dec.NextMessage()
	if err == nil {
		t.Fatalf("expected EOF on truncated payload, got nil")
	}
	if strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("small declared size must not trip the cap: %v", err)
	}
}
