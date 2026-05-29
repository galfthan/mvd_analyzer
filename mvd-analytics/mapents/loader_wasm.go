//go:build js && wasm

package mapents

import (
	"fmt"
	"syscall/js"

	"github.com/mvd-analyzer/mvd-analytics/loc"
)

// LoadForMap pulls the per-map entity JSON from the JS host via a
// synchronous callback. The host (worker.js) installs
// `fetchMapEntsSync(name)` performing a sync XHR against
// `mapents/<name>.json` — same pattern as fetchLocSync / fetchBspSync.
// Missing file ⇒ error ⇒ caller leaves the section absent.
func LoadForMap(mapName string) (*MapEntities, error) {
	base := loc.NormalizeMapName(mapName)

	fn := js.Global().Get("fetchMapEntsSync")
	if fn.IsUndefined() || fn.Type() != js.TypeFunction {
		return nil, fmt.Errorf("mapents: host fetchMapEntsSync not available")
	}

	res := fn.Invoke(base)
	if res.IsNull() || res.IsUndefined() {
		return nil, fmt.Errorf("no map-entities for map %s", base)
	}

	switch res.Type() {
	case js.TypeObject:
		length := res.Length()
		if length == 0 {
			return nil, fmt.Errorf("no map-entities for map %s", base)
		}
		data := make([]byte, length)
		if n := js.CopyBytesToGo(data, res); n != length {
			return nil, fmt.Errorf("mapents: short read from host (%d/%d) for map %s", n, length, base)
		}
		return parse(base, data)
	case js.TypeString:
		text := res.String()
		if text == "" {
			return nil, fmt.Errorf("no map-entities for map %s", base)
		}
		return parse(base, []byte(text))
	default:
		return nil, fmt.Errorf("mapents: unexpected host return type for map %s", base)
	}
}
