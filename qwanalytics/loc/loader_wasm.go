//go:build js && wasm

package loc

import (
	"fmt"
	"syscall/js"
)

// LoadForMap pulls the .loc file from the JS host via a synchronous
// callback. The host (worker.js) installs `fetchLocSync(name)` which
// performs a sync XHR against `locs/<name>.loc` — sync XHR is still
// permitted inside Web Workers, so this stays inline with the rest of
// the analysis pipeline without any API change to analyzeMVD.
func LoadForMap(mapName string) (*Finder, error) {
	base := NormalizeMapName(mapName)

	fn := js.Global().Get("fetchLocSync")
	if fn.IsUndefined() || fn.Type() != js.TypeFunction {
		return nil, fmt.Errorf("loc: host fetchLocSync not available")
	}

	res := fn.Invoke(base)
	if res.IsNull() || res.IsUndefined() {
		return nil, fmt.Errorf("no loc file for map %s", base)
	}
	text := res.String()
	if text == "" {
		return nil, fmt.Errorf("no loc file for map %s", base)
	}
	return buildFinder(base, []byte(text))
}
