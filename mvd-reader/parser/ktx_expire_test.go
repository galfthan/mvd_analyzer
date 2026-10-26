package parser

import "testing"

func TestTryEmitBackpackExpireHint(t *testing.T) {
	p := NewParser(nil)

	var captured *BackpackExpireHintEvent
	p.OnEvent(func(e Event) error {
		if h, ok := e.(*BackpackExpireHintEvent); ok {
			captured = h
		}
		return nil
	})

	if err := p.tryEmitBackpackExpireHint("//ktx expire 142\n", 120500); err != nil {
		t.Fatalf("tryEmitBackpackExpireHint: %v", err)
	}
	if captured == nil {
		t.Fatal("no hint event emitted")
	}
	if captured.BackpackEnt != 142 {
		t.Errorf("BackpackEnt = %d, want 142", captured.BackpackEnt)
	}
	if captured.TimeMs != 120500 {
		t.Errorf("TimeMs = %d, want 120500", captured.TimeMs)
	}
}

// The three RL/LG backpack directives share a prefix up to `//ktx ` and are
// matched by the same helper, so each matcher has to reject the other two.
func TestTryEmitBackpackExpireHint_SiblingDirectivesDoNotEmit(t *testing.T) {
	p := NewParser(nil)

	emitted := 0
	p.OnEvent(func(e Event) error {
		if _, ok := e.(*BackpackExpireHintEvent); ok {
			emitted++
		}
		return nil
	})

	for _, cmd := range []string{
		"//ktx drop 142 32 5\n",
		"//ktx bp 142 5\n",
		"//ktx took 12 30 5\n",
	} {
		if err := p.tryEmitBackpackExpireHint(cmd, 0); err != nil {
			t.Fatalf("tryEmitBackpackExpireHint(%q): %v", cmd, err)
		}
	}
	if emitted != 0 {
		t.Errorf("got %d expire events, want 0", emitted)
	}
}

func TestTryEmitBackpackExpireHint_MalformedSilentlyDropped(t *testing.T) {
	p := NewParser(nil)

	emitted := 0
	p.OnEvent(func(e Event) error {
		emitted++
		return nil
	})

	for _, cmd := range []string{
		"//ktx expire",
		"//ktx expire ",
		"//ktx expire garbage",
	} {
		if err := p.tryEmitBackpackExpireHint(cmd, 0); err != nil {
			t.Errorf("malformed %q returned error %v", cmd, err)
		}
	}
	if emitted != 0 {
		t.Errorf("got %d events, want 0 (all malformed inputs)", emitted)
	}
}

// The stufftext fan-out is the real entry point: a demo carrying all three
// backpack directives must produce one typed event each.
func TestTryEmitKtxHints_FansOutExpire(t *testing.T) {
	p := NewParser(nil)

	var drops, picks, expires int
	p.OnEvent(func(e Event) error {
		switch e.(type) {
		case *BackpackDropHintEvent:
			drops++
		case *BackpackPickupHintEvent:
			picks++
		case *BackpackExpireHintEvent:
			expires++
		}
		return nil
	})

	for _, cmd := range []string{
		"//ktx drop 142 32 5\n",
		"//ktx bp 142 3\n",
		"//ktx expire 142\n",
		"//wps 1 32 4 9\n",
	} {
		if err := p.tryEmitKtxHints(cmd, 0); err != nil {
			t.Fatalf("tryEmitKtxHints(%q): %v", cmd, err)
		}
	}
	if drops != 1 || picks != 1 || expires != 1 {
		t.Errorf("drops/picks/expires = %d/%d/%d, want 1/1/1", drops, picks, expires)
	}
}
