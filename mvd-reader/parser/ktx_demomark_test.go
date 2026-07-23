package parser

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// stuffTextPayload builds a svc_stufftext network-message payload
// (cmd byte + null-terminated string) for feeding parseNetworkMessage.
func stuffTextPayload(cmd string) []byte {
	b := []byte{mvd.SvcStuffText}
	b = append(b, []byte(cmd)...)
	return append(b, 0)
}

func captureDemoMark(t *testing.T, header mvd.MessageHeader, cmd string) *DemoMarkEvent {
	t.Helper()
	p := NewParser(nil)
	var captured *DemoMarkEvent
	p.OnEvent(func(e Event) error {
		if m, ok := e.(*DemoMarkEvent); ok {
			captured = m
		}
		return nil
	})
	msg := &mvd.DemoMessage{Header: header, Payload: stuffTextPayload(cmd), TimeMs: 435385}
	if err := p.parseNetworkMessage(msg); err != nil {
		t.Fatalf("parseNetworkMessage: %v", err)
	}
	return captured
}

func TestDemoMark_SingleBlockAttributesSlot(t *testing.T) {
	got := captureDemoMark(t, mvd.MessageHeader{MessageType: mvd.DemSingle, PlayerNum: 3}, "//demomark\n")
	if got == nil {
		t.Fatal("no DemoMarkEvent emitted")
	}
	if got.PlayerSlot != 3 {
		t.Errorf("PlayerSlot = %d, want 3", got.PlayerSlot)
	}
	if got.Label != "" {
		t.Errorf("Label = %q, want empty", got.Label)
	}
	if got.TimeMs != 435385 {
		t.Errorf("TimeMs = %d, want 435385", got.TimeMs)
	}
}

func TestDemoMark_ArgumentTailCaptured(t *testing.T) {
	got := captureDemoMark(t, mvd.MessageHeader{MessageType: mvd.DemSingle, PlayerNum: 0}, "//demomark 0 round-07\n")
	if got == nil {
		t.Fatal("no DemoMarkEvent emitted")
	}
	if got.PlayerSlot != 0 {
		t.Errorf("PlayerSlot = %d, want 0", got.PlayerSlot)
	}
	if got.Label != "0 round-07" {
		t.Errorf("Label = %q, want %q", got.Label, "0 round-07")
	}
}

func TestDemoMark_BroadcastBlockHasNoSlot(t *testing.T) {
	got := captureDemoMark(t, mvd.MessageHeader{MessageType: mvd.DemAll}, "//demomark\n")
	if got == nil {
		t.Fatal("no DemoMarkEvent emitted")
	}
	if got.PlayerSlot != -1 {
		t.Errorf("PlayerSlot = %d, want -1 for a non-slot-addressed block", got.PlayerSlot)
	}
}

func TestDemoMark_PrefixWithoutBoundaryNotMatched(t *testing.T) {
	got := captureDemoMark(t, mvd.MessageHeader{MessageType: mvd.DemSingle, PlayerNum: 1}, "//demomarkX\n")
	if got != nil {
		t.Errorf("//demomarkX matched as a demo mark: %+v", got)
	}
}
