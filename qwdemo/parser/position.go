package parser

import (
	"github.com/mvd-analyzer/qwdemo/mvd"
)

// PlayerPositionEvent is emitted when a player position is updated
type PlayerPositionEvent struct {
	PlayerNum int
	Origin    [3]float32 // X, Y, Z world coordinates
	Angles    [3]float32 // Pitch, Yaw, Roll
	Time      float64
}

func (e *PlayerPositionEvent) EventType() EventType { return EventPlayerInfo }
func (e *PlayerPositionEvent) EventTime() float64   { return e.Time }

// parsePlayerInfo parses svc_playerinfo message and emits position events
func (p *Parser) parsePlayerInfo(r *mvd.BufferReader, time float64, floatCoords bool) error {
	playerNum, err := r.ReadByte()
	if err != nil {
		return err
	}

	// Bounds check
	if playerNum >= mvd.MaxClients {
		return skipPlayerInfoRemainder(r, floatCoords)
	}

	flags, err := r.ReadUint16()
	if err != nil {
		return err
	}

	// Skip frame byte
	if err := r.Skip(1); err != nil {
		return err
	}

	// Get stored position for this player (for delta updates)
	origin := p.playerPositions[playerNum]

	// Read origin components (delta encoded - only present if flag is set)
	for i := 0; i < 3; i++ {
		if flags&(mvd.DFOrigin<<i) != 0 {
			var coord float32
			if floatCoords {
				coord, err = r.ReadFloatCoord()
			} else {
				coord, err = r.ReadCoord()
			}
			if err != nil {
				return err
			}
			origin[i] = coord
		}
	}

	// Store updated position
	p.playerPositions[playerNum] = origin

	// Read angle components
	var angles [3]float32
	for i := 0; i < 3; i++ {
		if flags&(mvd.DFAngles<<i) != 0 {
			angles[i], err = r.ReadAngle16()
			if err != nil {
				return err
			}
		}
	}

	// Skip remaining optional fields
	if flags&mvd.DFModel != 0 {
		if err := r.Skip(1); err != nil {
			return err
		}
	}
	if flags&mvd.DFSkinNum != 0 {
		if err := r.Skip(1); err != nil {
			return err
		}
	}
	if flags&mvd.DFEffects != 0 {
		if err := r.Skip(1); err != nil {
			return err
		}
	}
	if flags&mvd.DFWeaponFrame != 0 {
		if err := r.Skip(1); err != nil {
			return err
		}
	}

	// Only emit position event if we have valid position data
	// (skip if all coordinates are zero - likely uninitialized)
	if origin[0] != 0 || origin[1] != 0 || origin[2] != 0 {
		return p.emit(&PlayerPositionEvent{
			PlayerNum: int(playerNum),
			Origin:    origin,
			Angles:    angles,
			Time:      time,
		})
	}

	return nil
}

// skipPlayerInfoRemainder skips the rest of a playerinfo message after reading player num
func skipPlayerInfoRemainder(r *mvd.BufferReader, floatCoords bool) error {
	flags, err := r.ReadUint16()
	if err != nil {
		return err
	}
	r.Skip(1) // frame

	// Origin components
	for i := 0; i < 3; i++ {
		if flags&(mvd.DFOrigin<<i) != 0 {
			if floatCoords {
				r.Skip(4)
			} else {
				r.Skip(2)
			}
		}
	}
	// Angle components
	for i := 0; i < 3; i++ {
		if flags&(mvd.DFAngles<<i) != 0 {
			r.Skip(2) // angle16
		}
	}
	if flags&mvd.DFModel != 0 {
		r.Skip(1)
	}
	if flags&mvd.DFSkinNum != 0 {
		r.Skip(1)
	}
	if flags&mvd.DFEffects != 0 {
		r.Skip(1)
	}
	if flags&mvd.DFWeaponFrame != 0 {
		r.Skip(1)
	}
	return nil
}
