package analyzer

import (
	"github.com/mvd-analyzer/mvd-reader/events"
)

// prefetchBatch is the producer's unit of hand-off: a run of events and,
// on the last batch, the error that ended the stream (io.EOF included).
type prefetchBatch struct {
	events []events.Event
	err    error
}

// prefetchBatchSize is events per hand-off: large enough that the channel
// costs nothing next to the events' parse, small enough that the consumer
// starts at once.
const prefetchBatchSize = 512

// prefetchSource reads an events.Source on its own goroutine, so with
// Context.Parallel the demo's decompress + parse overlaps the analyzers'
// OnEvent instead of alternating with it — the event pass is otherwise
// one core's sum of both. The consumer sees the identical event sequence
// and the identical terminating error, after every event before it.
//
// Sharing is safe because the event pass only reads what a Source hands
// out, and the parser never touches an event after queueing it —
// TestParallelMatchesSerial run with -race checks both.
type prefetchSource struct {
	ch   chan prefetchBatch
	stop chan struct{}
	cur  []events.Event
	head int
	err  error
}

func newPrefetchSource(src events.Source) *prefetchSource {
	p := &prefetchSource{
		ch:   make(chan prefetchBatch, 4),
		stop: make(chan struct{}),
	}
	go func() {
		defer close(p.ch)
		for {
			b := prefetchBatch{events: make([]events.Event, 0, prefetchBatchSize)}
			for len(b.events) < prefetchBatchSize && b.err == nil {
				e, err := src.Next()
				if err != nil {
					b.err = err
					break
				}
				b.events = append(b.events, e)
			}
			select {
			case p.ch <- b:
			case <-p.stop:
				return
			}
			if b.err != nil {
				return
			}
		}
	}()
	return p
}

// Next returns the source's next event, or its terminating error once
// every event before it has been returned.
func (p *prefetchSource) Next() (events.Event, error) {
	for p.head >= len(p.cur) {
		if p.err != nil {
			return nil, p.err
		}
		b := <-p.ch
		p.cur, p.head, p.err = b.events, 0, b.err
	}
	e := p.cur[p.head]
	p.cur[p.head] = nil
	p.head++
	return e, nil
}

// halt stops the producer when the consumer abandons the stream early (an
// analyzer error). After a drained stream it is a no-op.
func (p *prefetchSource) halt() {
	close(p.stop)
}
