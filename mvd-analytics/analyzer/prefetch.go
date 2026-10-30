package analyzer

import (
	"io"

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
	done chan struct{}
	cur  []events.Event
	head int
	err  error
}

func newPrefetchSource(src events.Source) *prefetchSource {
	p := &prefetchSource{
		ch:   make(chan prefetchBatch, 4),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go func() {
		defer close(p.done)
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
// analyzer error) and waits for it to leave the Source, so the caller may
// close it. After a drained stream it only waits out the goroutine's exit.
func (p *prefetchSource) halt() {
	close(p.stop)
	<-p.done
}

// readAheadChunk is how much inflated demo the decompressor hands over at
// a time.
const readAheadChunk = 256 << 10

type readAheadChunkMsg struct {
	buf []byte
	err error
}

// readAhead runs a reader (the demo's gunzip) on its own goroutine, a
// chunk ahead of the consumer — the first stage of Parallel's pipeline,
// in front of prefetchSource. Bytes and the terminating error (io.EOF, a
// truncated gzip, ErrDecompressedTooLarge, a cancelled ctx) reach the
// consumer in order. Buffers cycle through free, so the whole demo is
// never resident twice.
type readAhead struct {
	ch   chan readAheadChunkMsg
	free chan []byte
	stop chan struct{}
	done chan struct{}
	cur  []byte
	buf  []byte
	err  error
}

func newReadAhead(r io.Reader) *readAhead {
	a := &readAhead{
		ch:   make(chan readAheadChunkMsg, 2),
		free: make(chan []byte, 3),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go func() {
		defer close(a.done)
		defer close(a.ch)
		for {
			var buf []byte
			select {
			case buf = <-a.free:
			default:
				buf = make([]byte, readAheadChunk)
			}
			n := 0
			var err error
			for n < len(buf) && err == nil {
				var m int
				m, err = r.Read(buf[n:])
				n += m
			}
			select {
			case a.ch <- readAheadChunkMsg{buf: buf[:n], err: err}:
			case <-a.stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return a
}

func (a *readAhead) Read(p []byte) (int, error) {
	for len(a.cur) == 0 {
		if a.err != nil {
			return 0, a.err
		}
		if a.buf != nil {
			select {
			case a.free <- a.buf[:cap(a.buf)]:
			default:
			}
		}
		m := <-a.ch
		a.buf, a.cur, a.err = m.buf, m.buf, m.err
	}
	n := copy(p, a.cur)
	a.cur = a.cur[n:]
	return n, nil
}

// halt stops the decompressor goroutine and waits until it is out of the
// underlying reader, which the caller closes next.
func (a *readAhead) halt() {
	close(a.stop)
	<-a.done
}
