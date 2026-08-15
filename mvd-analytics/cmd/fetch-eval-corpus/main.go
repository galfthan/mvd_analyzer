// fetch-eval-corpus downloads the N most recent hub demos per map into a
// directory, writing a manifest.json of the fetched game ids so an eval
// corpus is pinned and re-fetchable. Companion to cmd/qw-recon-eval.
//
// Usage:
//
//	go run ./mvd-analytics/cmd/fetch-eval-corpus -maps dm2,dm3 -limit 30 -out /path/to/corpus
//
// Hub credentials come from the hubfetch env vars (HUB_SUPABASE_URL,
// HUB_SUPABASE_KEY, optional HUB_CDN_URL).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
)

func main() {
	maps := flag.String("maps", "dm2,dm3", "comma-separated map filters")
	limit := flag.Int("limit", 30, "most recent demos per map")
	out := flag.String("out", "", "output directory (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "fetch-eval-corpus: -out is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fetch-eval-corpus:", err)
		os.Exit(1)
	}

	client := hubfetch.NewClient()
	type row struct {
		GameID int    `json:"gameId"`
		Map    string `json:"map"`
		Mode   string `json:"mode"`
	}
	var manifest []row

	for _, m := range strings.Split(*maps, ",") {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		res, err := client.Search(context.Background(), hubfetch.SearchParams{Map: m, Limit: *limit})
		if err != nil {
			fmt.Fprintf(os.Stderr, "search %s: %v\n", m, err)
			os.Exit(1)
		}
		games, _ := res.(map[string]any)["games"].([]any)
		fmt.Printf("%s: %d games\n", m, len(games))
		for _, g := range games {
			gm, _ := g.(map[string]any)
			id, _ := gm["id"].(float64)
			mode, _ := gm["mode"].(string)
			gameID := int(id)
			dest := filepath.Join(*out, fmt.Sprintf("%s_%d.mvd.gz", m, gameID))
			manifest = append(manifest, row{GameID: gameID, Map: m, Mode: mode})
			if _, err := os.Stat(dest); err == nil {
				fmt.Printf("  %d cached\n", gameID)
				continue
			}
			info, err := client.Resolve(gameID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  resolve %d: %v\n", gameID, err)
				continue
			}
			data, err := client.Download(info)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  download %d: %v\n", gameID, err)
				continue
			}
			if err := os.WriteFile(dest, data, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "  write %d: %v\n", gameID, err)
				os.Exit(1)
			}
			fmt.Printf("  %d fetched (%d KB, mode %s)\n", gameID, len(data)/1024, mode)
		}
	}

	mf, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(*out, "manifest.json"), append(mf, '\n'), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest:", err)
		os.Exit(1)
	}
	fmt.Printf("manifest: %d demos\n", len(manifest))
}
