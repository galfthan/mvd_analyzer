//go:build js && wasm

package items

import (
	"encoding/json"
	"fmt"
	"syscall/js"
)

// LoadForMap pulls the items JSON from the JS host via a synchronous
// callback. The host (worker.js) installs `fetchItemsSync(name)` which
// performs a sync XHR against `items/<name>.json` — the same pattern
// as the loc corpus.
func LoadForMap(mapName string) ([]MapItem, error) {
	base := NormalizeMapName(mapName)

	fn := js.Global().Get("fetchItemsSync")
	if fn.IsUndefined() || fn.Type() != js.TypeFunction {
		return nil, fmt.Errorf("items: host fetchItemsSync not available")
	}

	res := fn.Invoke(base)
	if res.IsNull() || res.IsUndefined() {
		return nil, fmt.Errorf("no items file for map %s", base)
	}
	text := res.String()
	if text == "" {
		return nil, fmt.Errorf("no items file for map %s", base)
	}
	var f fileFormat
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		return nil, fmt.Errorf("items: decode %s: %w", base, err)
	}
	return f.Items, nil
}
