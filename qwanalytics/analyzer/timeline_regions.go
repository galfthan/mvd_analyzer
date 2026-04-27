package analyzer

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/qwanalytics/config"
	"github.com/mvd-analyzer/qwanalytics/loc"
)

// ============================================================================
// REGION CONTROL CONFIGURATION
//
// Region control tracks which team controls key areas of the map.
// There are two layers of configuration:
//
// 1. AUTO-DETECTION (controlKeywords):
//    Any loc name containing one of these keywords (as a dot/space-separated
//    token) becomes a tracked region. If multiple locs share the same keyword
//    but are far apart (>800 world units), they are split into separate regions
//    named by their distinguishing prefix (e.g., "high.RL" and "low.RL").
//
//    Maps with no RA loc fall back to YA: the auto-detector swaps RA→YA
//    for that map only, so duel maps like dm6 (no RA) still get a tracked
//    armor region instead of an empty one.
//
// 2. MAP-SPECIFIC CUSTOM REGIONS (config/regions/<map>.json):
//    Per-map overrides ship as embedded JSON files. Each entry has a
//    display name and a list of loc names to include; those locs are
//    excluded from auto-detection so they don't get merged with
//    keyword-based regions. To find loc names for a map, check the
//    embedded .loc corpus (qwanalytics/loc/data/<map>.loc) — variables
//    like $loc_name_ra resolve to "RA" and $loc_name_separator to ".".
//    The loc names visible in the browser's Region Control panel are the
//    canonical post-substitution form, and the panel's Save button emits
//    JSON in the exact shape regions/<map>.json uses.
// ============================================================================

// controlKeywords lists the item types that are auto-detected as regions.
// Add entries here to track additional item types across all maps.
var controlKeywords = map[string]bool{
	"RA": true, "RL": true, "LG": true, "QUAD": true,
}

type locWithKeyword struct {
	loc     loc.Location
	keyword string
}

// buildControlRegions groups locations by item keyword and clusters spatially
func (a *TimelineAnalyzer) buildControlRegions() []ControlRegion {
	locs := a.locFinder.Locations()
	if len(locs) == 0 {
		return nil
	}

	// Get map name for map-specific customization
	mapName := ""
	if a.core != nil && a.core.DemoInfo != nil && a.core.DemoInfo.Map != "" {
		mapName = strings.ToLower(a.core.DemoInfo.Map)
		if idx := strings.LastIndex(mapName, "/"); idx >= 0 {
			mapName = mapName[idx+1:]
		}
		mapName = strings.TrimSuffix(mapName, ".bsp")
	}

	// Build the per-map keyword set. If the map has no RA loc anywhere,
	// promote YA in RA's place so duel maps like dm6 (YA-only) still get
	// a tracked armor region. Custom regions are evaluated independently;
	// this fallback only affects the keyword-based auto-detector.
	keywordSet := make(map[string]bool, len(controlKeywords))
	for k, v := range controlKeywords {
		keywordSet[k] = v
	}
	if !mapHasKeyword(locs, "RA") {
		delete(keywordSet, "RA")
		keywordSet["YA"] = true
	}

	// Build set of locs consumed by custom regions (so they're excluded from auto-detection)
	// and the set of auto-detect keywords that a custom region has fully claimed.
	// A custom region claims a keyword whenever its name (uppercased) matches
	// an entry in keywordSet — in that case the auto-detector skips that
	// keyword entirely so the curated definition is the single source of truth
	// (no leftover one-loc clusters competing under the same name).
	customConsumed := make(map[string]bool)
	customClaimedKeywords := make(map[string]bool)
	customDefs := config.RegionsForMap(mapName)
	for _, cr := range customDefs {
		for _, ln := range cr.Locs {
			customConsumed[ln] = true
		}
		if keywordSet[strings.ToUpper(cr.Name)] {
			customClaimedKeywords[strings.ToUpper(cr.Name)] = true
		}
	}

	// Group locations by any matching keyword token in their name
	// e.g., "cellar.RL" matches RL, "RA.stairs" matches RA
	groups := make(map[string][]locWithKeyword)

	for _, l := range locs {
		// Skip locs consumed by custom regions
		if customConsumed[l.Name] {
			continue
		}

		tokens := strings.FieldsFunc(l.Name, func(r rune) bool {
			return r == '.' || r == ' '
		})
		for _, token := range tokens {
			upper := strings.ToUpper(token)
			if keywordSet[upper] {
				groups[upper] = append(groups[upper], locWithKeyword{loc: l, keyword: upper})
				break // Only match first keyword per location
			}
		}
	}

	var regions []ControlRegion

	// Build custom regions from map-specific definitions
	locByName := make(map[string][]loc.Location)
	for _, l := range locs {
		locByName[l.Name] = append(locByName[l.Name], l)
	}
	for _, cr := range customDefs {
		region := ControlRegion{Name: cr.Name}
		var sumX, sumY float32
		var count int
		for _, ln := range cr.Locs {
			for _, l := range locByName[ln] {
				region.Points = append(region.Points, MapLocation{
					X: l.X, Y: l.Y, Z: l.Z, Name: l.Name,
				})
				sumX += l.X
				sumY += l.Y
				count++
			}
		}
		if count > 0 {
			region.CentroidX = sumX / float32(count)
			region.CentroidY = sumY / float32(count)
			regions = append(regions, region)
		}
	}

	for keyword, locs := range groups {
		if len(locs) == 0 {
			continue
		}
		// A custom region with this exact name has full ownership of the
		// keyword — skip auto-detection so the curated list isn't padded
		// with leftover loc clusters under the same name.
		if customClaimedKeywords[keyword] {
			continue
		}
		clusters := clusterLocations(locs, 800)

		for _, cluster := range clusters {
			region := ControlRegion{}

			// Name the region
			if len(clusters) == 1 {
				region.Name = keyword
			} else {
				// Find most common second token for naming
				region.Name = nameCluster(keyword, cluster)
			}

			// Build points and centroid
			var sumX, sumY float32
			for _, lk := range cluster {
				region.Points = append(region.Points, MapLocation{
					X:    lk.loc.X,
					Y:    lk.loc.Y,
					Z:    lk.loc.Z,
					Name: lk.loc.Name,
				})
				sumX += lk.loc.X
				sumY += lk.loc.Y
			}
			region.CentroidX = sumX / float32(len(cluster))
			region.CentroidY = sumY / float32(len(cluster))

			regions = append(regions, region)
		}
	}

	// Sort regions by name for stable output
	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Name < regions[j].Name
	})

	return regions
}

// mapHasKeyword reports whether any loc name contains `keyword` as a
// dot/space-separated token (case-insensitive). Used to drive the
// RA→YA fallback for maps that ship no RA.
func mapHasKeyword(locs []loc.Location, keyword string) bool {
	upper := strings.ToUpper(keyword)
	for _, l := range locs {
		tokens := strings.FieldsFunc(l.Name, func(r rune) bool {
			return r == '.' || r == ' '
		})
		for _, t := range tokens {
			if strings.ToUpper(t) == upper {
				return true
			}
		}
	}
	return false
}

// clusterLocations groups locations by spatial proximity using single-linkage clustering
func clusterLocations(locs []locWithKeyword, threshold float64) [][]locWithKeyword {
	n := len(locs)
	if n == 0 {
		return nil
	}

	// Union-Find
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	threshSq := threshold * threshold
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			dx := float64(locs[i].loc.X - locs[j].loc.X)
			dy := float64(locs[i].loc.Y - locs[j].loc.Y)
			if dx*dx+dy*dy < threshSq {
				union(i, j)
			}
		}
	}

	// Group by root
	clusterMap := make(map[int][]locWithKeyword)
	for i, l := range locs {
		root := find(i)
		clusterMap[root] = append(clusterMap[root], l)
	}

	var result [][]locWithKeyword
	for _, c := range clusterMap {
		result = append(result, c)
	}
	return result
}

// nameCluster names a cluster based on the most common second token
func nameCluster(keyword string, cluster []locWithKeyword) string {
	// Find the most common non-keyword token across loc names in this cluster.
	// E.g., for keyword "RL", locs like "high.RL" → token "high", "low.RL" → token "low"
	keywordLower := strings.ToLower(keyword)
	tokenCounts := make(map[string]int)
	for _, lk := range cluster {
		tokens := strings.FieldsFunc(lk.loc.Name, func(r rune) bool {
			return r == '.' || r == ' '
		})
		for _, t := range tokens {
			lower := strings.ToLower(t)
			if lower != keywordLower {
				tokenCounts[lower]++
			}
		}
	}

	bestToken := ""
	bestCount := 0
	for token, count := range tokenCounts {
		if count > bestCount {
			bestCount = count
			bestToken = token
		}
	}

	if bestToken != "" {
		return bestToken + "." + keyword
	}
	return keyword
}
