package democache

import (
	"container/list"
	"sync"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// resultLRU is a small mutex-protected LRU of *result.Result keyed by
// SHA. Eliminates the gob-decode cost when a session of queries hits
// the same demo.
type resultLRU struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	idx      map[string]*list.Element
}

type lruEntry struct {
	key string
	val *result.Result
}

func newResultLRU(capacity int) *resultLRU {
	if capacity < 1 {
		capacity = 1
	}
	return &resultLRU{
		capacity: capacity,
		ll:       list.New(),
		idx:      make(map[string]*list.Element, capacity),
	}
}

func (l *resultLRU) get(key string) *result.Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.idx[key]; ok {
		l.ll.MoveToFront(e)
		return e.Value.(*lruEntry).val
	}
	return nil
}

func (l *resultLRU) put(key string, val *result.Result) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.idx[key]; ok {
		l.ll.MoveToFront(e)
		e.Value.(*lruEntry).val = val
		return
	}
	e := l.ll.PushFront(&lruEntry{key: key, val: val})
	l.idx[key] = e
	for l.ll.Len() > l.capacity {
		back := l.ll.Back()
		if back == nil {
			break
		}
		l.ll.Remove(back)
		delete(l.idx, back.Value.(*lruEntry).key)
	}
}
