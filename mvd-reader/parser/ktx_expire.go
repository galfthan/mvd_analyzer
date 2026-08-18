package parser

// BackpackExpireHintEvent is the typed representation of KTX's
// `//ktx expire <ent>` STUFFCMD_DEMOONLY directive
// (ktx/src/g_spawn.c:196-210):
//
//	void SUB_Remove(void)
//	{
//	        if (self && streq(self->classname, "backpack"))
//	        {
//	                if ((self->s.v.items == IT_ROCKET_LAUNCHER) || (self->s.v.items == IT_LIGHTNING))
//	                        stuffcmd_flags(world, STUFFCMD_DEMOONLY, "//ktx expire %d\n", NUM_FOR_EDICT(self));
//	        }
//	        ent_remove(self);
//	}
//
// It is the third member of the RL/LG backpack hint family and closes it:
// `//ktx drop` says a pack was made, `//ktx bp` says someone took it, and
// this says the server removed it untaken. `DropBackpack` arms
// `nextthink = time + 120; think = SUB_Remove` on every pack it spawns
// (ktx/src/items.c:2870-2872), so on a dropped pack this fires at the
// 120 s timeout and is positive, per-pack evidence that nobody picked it
// up — the one thing the absence of a `//ktx bp` cannot establish, since
// a demo can carry the drop hint and no pickup hints at all.
//
// The directive is stuffed at `world` rather than at a client (the KTX
// comment says so outright: "get away with 'world' because mvdsv will
// exit before test with STUFFCMD_DEMOONLY"), so it carries no player and
// needs none. `BackpackEnt` is the same edict-number namespace as
// BackpackDropHintEvent.BackpackEnt and BackpackPickupHintEvent.BackpackEnt
// — but edicts are recycled within a match, so a consumer joining on it
// must also use time, exactly as the drop/pickup join does.
type BackpackExpireHintEvent struct {
	BackpackEnt int // server edict of the removed backpack
	TimeMs      int32
}

func (e *BackpackExpireHintEvent) EventType() EventType { return EventBackpackExpireHint }
func (e *BackpackExpireHintEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *BackpackExpireHintEvent) EventTimeMs() int32   { return e.TimeMs }

const ktxExpirePrefix = "//ktx expire "

// tryEmitBackpackExpireHint scans a stuffcmd payload for `//ktx expire`
// and emits a typed BackpackExpireHintEvent on success. Returns nil
// silently on malformed input — the StuffTextEvent for the same command
// has already been emitted by the caller, so dropping a hint event is a
// soft failure.
func (p *Parser) tryEmitBackpackExpireHint(cmd string, timeMs int32) error {
	v, ok := parseKtxHintInts(cmd, ktxExpirePrefix, 1)
	if !ok {
		return nil
	}
	return p.emit(&BackpackExpireHintEvent{
		BackpackEnt: v[0],
		TimeMs:      timeMs,
	})
}
