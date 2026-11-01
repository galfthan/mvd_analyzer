package democache

import (
	"container/list"
	"sync"
)

// lru is a small mutex-protected LRU keyed by SHA. As the in-process
// Result cache (mem) it eliminates the gob-decode cost when a session of
// queries hits the same demo; as pages it keeps the encoded /result
// bodies.
type lru[V any] struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	idx      map[string]*list.Element
}

type lruEntry[V any] struct {
	key string
	val V
}

func newLRU[V any](capacity int) *lru[V] {
	if capacity < 1 {
		capacity = 1
	}
	return &lru[V]{
		capacity: capacity,
		ll:       list.New(),
		idx:      make(map[string]*list.Element, capacity),
	}
}

// get returns the value under key, or V's zero value (nil for the pointer
// and slice values cached here) when it is absent.
func (l *lru[V]) get(key string) V {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.idx[key]; ok {
		l.ll.MoveToFront(e)
		return e.Value.(*lruEntry[V]).val
	}
	var zero V
	return zero
}

// delete drops a key from the LRU if present. Used by RemoveDemo so an
// upload rejected by the parse-gate cannot be served from memory after its
// disk tiers are removed.
func (l *lru[V]) delete(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.idx[key]; ok {
		l.ll.Remove(e)
		delete(l.idx, key)
	}
}

func (l *lru[V]) put(key string, val V) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.idx[key]; ok {
		l.ll.MoveToFront(e)
		e.Value.(*lruEntry[V]).val = val
		return
	}
	e := l.ll.PushFront(&lruEntry[V]{key: key, val: val})
	l.idx[key] = e
	for l.ll.Len() > l.capacity {
		back := l.ll.Back()
		if back == nil {
			break
		}
		l.ll.Remove(back)
		delete(l.idx, back.Value.(*lruEntry[V]).key)
	}
}
