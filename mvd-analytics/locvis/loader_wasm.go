//go:build js && wasm

package locvis

import (
	"syscall/js"

	"github.com/mvd-analyzer/mvd-analytics/loc"
)

// LoadForMap returns a Finder for the given map. The loc corpus is
// always required (loc.LoadForMap routes through the host's
// fetchLocSync). The BSP is best-effort: if the host has not installed
// fetchBspSync, or returns null (e.g. 404), the Finder is returned
// with no BSP and FindNearest degenerates to V1.
//
// fetchBspSync(name string) -> Uint8Array | null is the host-installed
// synchronous loader. The worker.js implementation lives at
// mvd-web/static/worker.js and mirrors fetchLocSync exactly.
func LoadForMap(mapName string) (*Finder, error) {
	base, err := loc.LoadForMap(mapName)
	if err != nil {
		return nil, err
	}
	return newFinder(base, fetchBspBytes(loc.NormalizeMapName(mapName))), nil
}

func fetchBspBytes(normalisedMap string) []byte {
	fn := js.Global().Get("fetchBspSync")
	if fn.IsUndefined() || fn.Type() != js.TypeFunction {
		return nil
	}
	res := fn.Invoke(normalisedMap)
	if res.IsNull() || res.IsUndefined() {
		return nil
	}
	if res.Type() != js.TypeObject {
		return nil
	}
	length := res.Length()
	if length <= 0 {
		return nil
	}
	data := make([]byte, length)
	if n := js.CopyBytesToGo(data, res); n != length {
		return nil
	}
	return data
}
