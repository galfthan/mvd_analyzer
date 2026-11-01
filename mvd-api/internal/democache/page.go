package democache

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"

	"github.com/mvd-analyzer/mvd-analytics/view"
)

// pageLRUSize is how many encoded /result bodies the process keeps (~1 MB
// each gzipped on a 4on4).
const pageLRUSize = 16

// PageResult returns the demo's Result as the web page reads it
// (view.PageResult), as gzipped JSON: the body GET /v1/demos/{id}/result
// sends. Marshalling and compressing 4–6 MB costs a couple of hundred ms,
// so each body is built once per demo, under the parse semaphore like any
// other heavy cold operation, and kept in memory. Not on disk: the body is
// view output (the playerStats overlay), which can change without a schema
// bump, and a restart — every deploy — clears it.
//
// /los writes LOS/PVS into the shared Result under the demo's LOS lock, and
// view.PageResult's copy reads every player stream, so the copy is taken
// under that lock (with no semaphore held, the order EnsureLOS uses) and
// leaves them out; the marshal then reads only the copy's own fields and
// data nothing writes.
func (c *Cache) PageResult(ctx context.Context, id DemoID) ([]byte, CacheMeta, error) {
	c.ensureInit()
	res, meta, err := c.GetResult(ctx, id)
	if err != nil {
		return nil, meta, err
	}
	sha := meta.SHA256
	if body := c.pages.get(sha); body != nil {
		return body, meta, nil
	}
	unlock := c.pageLocks.Lock(sha)
	defer unlock()
	if body := c.pages.get(sha); body != nil {
		return body, meta, nil
	}

	unlockLOS := c.losLocks.Lock(sha)
	page := view.PageResult(res)
	unlockLOS()

	release, err := c.acquireParse(ctx)
	if err != nil {
		return nil, meta, err
	}
	defer release()
	raw, err := json.Marshal(page)
	if err != nil {
		return nil, meta, fmt.Errorf("marshal page result: %w", err)
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if _, err := zw.Write(raw); err != nil {
		return nil, meta, fmt.Errorf("compress page result: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, meta, fmt.Errorf("compress page result: %w", err)
	}
	body := buf.Bytes()
	c.pages.put(sha, body)
	return body, meta, nil
}
