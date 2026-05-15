// mvd-api hosts the qwanalytics view surface over HTTP REST, backed by
// an on-disk two-tier cache that fetches demos from hub.quakeworld.nu
// on demand.
//
// Usage:
//
//	mvd-api [flags]
//	mvd-api version
//
// Flags:
//
//	-addr        listen address (default ":8080")
//	-cache-dir   on-disk cache root (default $XDG_CACHE_HOME/qw-mvd or ~/.cache/qw-mvd)
//	-log-format  text | json (default "text")
//
// See mvd-api/README.md for the endpoint surface.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"hash":      GitHash,
			"tag":       GitTag,
			"buildDate": BuildDate,
		})
		return
	}
	if err := runServe(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "mvd-api: %v\n", err)
		os.Exit(1)
	}
}
