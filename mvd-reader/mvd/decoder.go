package mvd

import (
	"errors"
	"fmt"
	"io"
)

var (
	// ErrEndOfDemo is returned when the demo ends normally
	ErrEndOfDemo = errors.New("end of demo")
)

// maxBlockSize caps the payload size a single dem_* message may declare on the
// wire before ReadBytes allocates. The size field is an attacker-controlled
// uint32, and ReadBytes does make([]byte, n) up front, so an unbounded value
// forces a ~4 GiB allocation from a 4-byte crafted header. mvdsv writes MVD
// messages out of an 8 KiB buffer (MAX_MVD_SIZE = MSG_BUF_SIZE(8192) - 100,
// bothdefs.h:27-35), so legitimate blocks are at most a few KiB; 8 MiB is ~1000x
// headroom for any conceivable extension while still refusing hostile input.
const maxBlockSize = 8 << 20

// Decoder reads MVD demo messages from a stream
type Decoder struct {
	reader *BinaryReader
	// timeMs is the canonical cumulative demo time in integer milliseconds.
	// Each MVD message carries a 1-byte ms delta; accumulating as int32
	// keeps the running total exact (1/1000 is not representable in base-2
	// float, so float seconds drift). int32 holds ±24.8 days — plenty.
	timeMs      int32
	extensions  *Extensions
	floatCoords bool
}

// Extensions holds detected protocol extensions
type Extensions struct {
	FTE  uint32
	FTE2 uint32
	MVD1 uint32
}

// NewDecoder creates a new MVD decoder
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{
		reader:     NewBinaryReader(r),
		extensions: &Extensions{},
	}
}

// SetExtensions sets the protocol extensions (called after parsing svc_serverdata)
func (d *Decoder) SetExtensions(ext *Extensions) {
	d.extensions = ext
	d.floatCoords = (ext.FTE&FTEPextFloatCoords != 0) || (ext.MVD1&MVDPext1FloatCoords != 0)
}

// FloatCoords returns true if float coordinates are enabled
func (d *Decoder) FloatCoords() bool {
	return d.floatCoords
}

// NextMessage reads the next demo message from the stream
func (d *Decoder) NextMessage() (*DemoMessage, error) {
	// Read time delta (1 byte)
	timeDelta, err := d.reader.ReadByte()
	if err != nil {
		if err == io.EOF {
			return nil, ErrEndOfDemo
		}
		return nil, err
	}

	// Update cumulative time. Delta is 1 byte in milliseconds; integer
	// accumulation keeps the running total exact.
	d.timeMs += int32(timeDelta)

	// Read message type byte
	typeByte, err := d.reader.ReadByte()
	if err != nil {
		return nil, err
	}

	// Extract message type (bits 0-2) and player number (bits 3-7)
	messageType := typeByte & 0x07
	playerNum := int(typeByte >> 3)

	msg := &DemoMessage{
		Header: MessageHeader{
			TimeDelta:   timeDelta,
			MessageType: messageType,
			PlayerNum:   playerNum,
		},
		TimeMs: d.timeMs,
	}

	// Handle each message type
	switch messageType {
	case DemSet:
		// Read sequence numbers (8 bytes total)
		_, err := d.reader.ReadUint32() // incoming sequence
		if err != nil {
			return nil, err
		}
		_, err = d.reader.ReadUint32() // outgoing sequence
		if err != nil {
			return nil, err
		}
		// No payload for dem_set
		msg.Payload = nil

	case DemMultiple:
		// Read player mask (4 bytes)
		playerMask, err := d.reader.ReadUint32()
		if err != nil {
			return nil, err
		}
		msg.PlayerMask = playerMask

		// Read payload size (4 bytes)
		size, err := d.reader.ReadUint32()
		if err != nil {
			return nil, err
		}
		if size > maxBlockSize {
			return nil, fmt.Errorf("dem_multiple block size %d exceeds maximum %d at stream offset %d", size, maxBlockSize, d.reader.Offset())
		}

		// Read payload
		payload, err := d.reader.ReadBytes(int(size))
		if err != nil {
			return nil, err
		}
		msg.Payload = payload

	case DemSingle, DemStats, DemAll, DemRead:
		// Same wire layout for all four: size-prefixed payload. For
		// dem_single/dem_stats the player number is already extracted
		// from the type byte.

		// Read payload size (4 bytes)
		size, err := d.reader.ReadUint32()
		if err != nil {
			return nil, err
		}
		if size > maxBlockSize {
			return nil, fmt.Errorf("dem block size %d exceeds maximum %d at stream offset %d", size, maxBlockSize, d.reader.Offset())
		}

		// Read payload
		payload, err := d.reader.ReadBytes(int(size))
		if err != nil {
			return nil, err
		}
		msg.Payload = payload

	case DemCmd:
		// User command - QWD only, skip in MVD
		// This shouldn't appear in MVD files, but handle it just in case
		return nil, errors.New("unexpected dem_cmd in MVD file")

	default:
		return nil, fmt.Errorf("unknown message type %d at stream offset %d", messageType, d.reader.Offset())
	}

	return msg, nil
}

// IsHiddenMessage returns true if this is a hidden message (dem_multiple with player_mask == 0)
func (m *DemoMessage) IsHiddenMessage() bool {
	return m.Header.MessageType == DemMultiple && m.PlayerMask == 0
}
