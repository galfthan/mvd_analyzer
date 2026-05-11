package democache

import (
	"github.com/mvd-analyzer/qwanalytics/result"
)

// inflightEntry is the shared state for a single in-flight loadResult.
// Multiple goroutines racing the same SHA wait on done, then read the
// terminal triplet.
type inflightEntry struct {
	done   chan struct{}
	result *result.Result
	meta   CacheMeta
	err    error
}

// getOrCompute serialises concurrent requests for the same SHA: the
// first goroutine runs compute, subsequent goroutines wait and observe
// the same result. Once compute returns, the inflight entry is removed
// — the next request after that pays its own compute cost.
func (c *Cache) getOrCompute(sha string, compute func() (*result.Result, CacheMeta, error)) (*result.Result, CacheMeta, error) {
	e := &inflightEntry{done: make(chan struct{})}
	actual, loaded := c.inflight.LoadOrStore(sha, e)
	if loaded {
		existing := actual.(*inflightEntry)
		<-existing.done
		return existing.result, existing.meta, existing.err
	}
	defer func() {
		c.inflight.Delete(sha)
		close(e.done)
	}()
	e.result, e.meta, e.err = compute()
	return e.result, e.meta, e.err
}
