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
	panic  *goroutinePanic
}

// goroutinePanic carries a panic off a pipeline goroutine to the
// consumer, which re-raises it on its own goroutine. Unforwarded, a
// parser bug on a malformed demo would kill the whole process instead of
// reaching the caller's recover (mvd-api's singleflight turns it into a
// 500).
type goroutinePanic struct{ value any }

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
// Sharing is safe because the parser does not touch an event after
// queueing it — with one exception: ServerDataEvent.Data is the parser's
// own record, whose MapFile it fills in from the model list after
// emitting. That field is read only in Finalize, after the hand-off, so
// reading it in OnEvent would race. TestParallelMatchesSerial run with
// -race checks the rest.
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
		defer func() {
			if v := recover(); v != nil {
				select {
				case p.ch <- prefetchBatch{panic: &goroutinePanic{v}}:
				case <-p.stop:
				}
			}
		}()
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
		if b.panic != nil {
			panic(b.panic.value)
		}
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
	buf   []byte
	err   error
	panic *goroutinePanic
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
		defer func() {
			if v := recover(); v != nil {
				select {
				case a.ch <- readAheadChunkMsg{panic: &goroutinePanic{v}}:
				case <-a.stop:
				}
			}
		}()
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
		if m.panic != nil {
			panic(m.panic.value)
		}
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
