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
//
// fetchLocSync should return a Uint8Array (raw bytes). Loc files
// commonly contain high-bit-ASCII obfuscated item shorthands (e.g.
// "ssg" rendered as 0xf3 0xf3 0xe7) that aren't valid UTF-8;
// substituteVariables() expects the raw bytes so it can strip bit 7
// and recognise them as "ssg". A legacy string return is also
// accepted for backwards compatibility.
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

	// Preferred path: Uint8Array (preserves bytes 0x80-0xFF).
	if res.Type() == js.TypeObject {
		length := res.Length()
		if length > 0 {
			data := make([]byte, length)
			n := js.CopyBytesToGo(data, res)
			if n != length {
				return nil, fmt.Errorf("loc: short read from host (%d/%d) for map %s", n, length, base)
			}
			return buildFinder(base, data)
		}
		return nil, fmt.Errorf("no loc file for map %s", base)
	}

	// Legacy string return — bytes >= 0x80 may have already been
	// mangled to U+FFFD by UTF-8 decoding, but accept anyway.
	text := res.String()
	if text == "" {
		return nil, fmt.Errorf("no loc file for map %s", base)
	}
	return buildFinder(base, []byte(text))
}
