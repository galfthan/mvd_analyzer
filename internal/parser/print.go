package parser

import (
	"github.com/mvd-analyzer/internal/mvd"
)

// PrintEvent is emitted when a print message is received
type PrintEvent struct {
	Level   int
	Message string
	Time    float64
}

func (e *PrintEvent) EventType() EventType { return EventPrint }
func (e *PrintEvent) EventTime() float64   { return e.Time }

// parsePrint parses svc_print message
func (p *Parser) parsePrint(r *mvd.BufferReader, time float64) error {
	// Read print level
	level, err := r.ReadByte()
	if err != nil {
		return err
	}

	// Read message
	message, err := r.ReadString()
	if err != nil {
		return err
	}

	// Clean the message (remove Quake color codes)
	cleanedMessage := cleanString(message)

	// Emit event
	return p.emit(&PrintEvent{
		Level:   int(level),
		Message: cleanedMessage,
		Time:    time,
	})
}
