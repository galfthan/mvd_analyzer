package parser

import "testing"

func TestTryEmitBackpackDropHint_RLDrop(t *testing.T) {
	p := NewParser(nil)

	var captured *BackpackDropHintEvent
	p.OnEvent(func(e Event) error {
		if h, ok := e.(*BackpackDropHintEvent); ok {
			captured = h
		}
		return nil
	})

	if err := p.tryEmitBackpackDropHint("//ktx drop 142 32 5\n", 1.5); err != nil {
		t.Fatalf("tryEmitBackpackDropHint: %v", err)
	}
	if captured == nil {
		t.Fatal("no hint event emitted")
	}
	if captured.BackpackEnt != 142 || captured.ItemFlags != 32 || captured.PlayerEnt != 5 {
		t.Errorf("hint = %+v, want {142, 32, 5}", captured)
	}
	if captured.Time != 1.5 {
		t.Errorf("Time = %f, want 1.5", captured.Time)
	}
}

func TestTryEmitBackpackDropHint_LGDrop(t *testing.T) {
	p := NewParser(nil)

	var captured *BackpackDropHintEvent
	p.OnEvent(func(e Event) error {
		if h, ok := e.(*BackpackDropHintEvent); ok {
			captured = h
		}
		return nil
	})

	if err := p.tryEmitBackpackDropHint("//ktx drop 200 64 3", 0); err != nil {
		t.Fatalf("tryEmitBackpackDropHint: %v", err)
	}
	if captured == nil || captured.ItemFlags != 64 {
		t.Errorf("LG hint not emitted as expected: %+v", captured)
	}
}

func TestTryEmitBackpackDropHint_TookDoesNotEmit(t *testing.T) {
	p := NewParser(nil)

	var captured int
	p.OnEvent(func(e Event) error {
		if _, ok := e.(*BackpackDropHintEvent); ok {
			captured++
		}
		return nil
	})

	if err := p.tryEmitBackpackDropHint("//ktx took 12 30 5\n", 0); err != nil {
		t.Fatalf("tryEmitBackpackDropHint: %v", err)
	}
	if captured != 0 {
		t.Errorf("got %d hint events, want 0 (//ktx took is not a drop)", captured)
	}
}

func TestTryEmitBackpackDropHint_MalformedSilentlyDropped(t *testing.T) {
	p := NewParser(nil)

	emitted := 0
	p.OnEvent(func(e Event) error {
		emitted++
		return nil
	})

	for _, cmd := range []string{
		"//ktx drop garbage",
		"//ktx drop 1",
		"//ktx drop 1 2",
		"//ktx drop 1 abc 3",
	} {
		if err := p.tryEmitBackpackDropHint(cmd, 0); err != nil {
			t.Errorf("malformed %q returned error %v", cmd, err)
		}
	}
	if emitted != 0 {
		t.Errorf("got %d events, want 0 (all malformed inputs)", emitted)
	}
}
