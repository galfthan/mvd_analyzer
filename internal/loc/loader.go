package loc

import (
	"bufio"
	"embed"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed locs/*.loc
var locFiles embed.FS

// Variable substitutions for loc file parsing
// These match the ezQuake teamplay_locfiles.c definitions
var locVariables = map[string]string{
	"$loc_name_ra":              "RA",
	"$loc_name_ya":              "YA",
	"$loc_name_ga":              "GA",
	"$loc_name_mh":              "MH",
	"$loc_name_quad":            "Quad",
	"$loc_name_pent":            "Pent",
	"$loc_name_ring":            "Ring",
	"$loc_name_separator":       " ",
	"$loc_name_separatorlow":    " low",
	"$loc_name_separatorhigh":   " high",
	"$loc_name_separatorup":     " up",
	"$loc_name_separatorstairs": " stairs",
	"$loc_name_separatorwall":   " wall",
	"$loc_name_separatorway":    " way",
	"$loc_name_separatorlift":   " lift",
	"$loc_name_separatorentry":  " entry",
	"$loc_name_separatorbutton": " button",
	"$loc_name_separatorroof":   " roof",
	"$loc_name_separatortunnel": " tunnel",
	"$loc_name_separatorbox":    " box",
	"$loc_name_separatorrox":    " rox",
	"$loc_name_separatorledge":  " ledge",
	"$loc_name_separatorlifts":  " lifts",
	"$loc_name_separatorgl":     " GL",
	"$loc_name_separatorlg":     " LG",
}

// LoadForMap loads the loc file for a given map name
// The map name can include path (e.g., "maps/dm2") and will be normalized
func LoadForMap(mapName string) (*Finder, error) {
	// Normalize map name: extract base name without path or extension
	baseName := filepath.Base(mapName)
	baseName = strings.TrimSuffix(baseName, ".bsp")
	baseName = strings.ToLower(baseName)

	// Map aliases: some maps share the same layout/locs
	mapAliases := map[string]string{
		"phantombase": "phantoma",
	}
	if alias, ok := mapAliases[baseName]; ok {
		baseName = alias
	}

	filename := fmt.Sprintf("locs/%s.loc", baseName)
	data, err := locFiles.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("no loc file for map %s: %w", baseName, err)
	}

	locations, err := parseLoc(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse loc file for %s: %w", baseName, err)
	}

	return &Finder{
		mapName:   baseName,
		locations: locations,
	}, nil
}

// AvailableMaps returns a list of map names that have loc files
func AvailableMaps() ([]string, error) {
	entries, err := locFiles.ReadDir("locs")
	if err != nil {
		return nil, err
	}

	var maps []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".loc") {
			mapName := strings.TrimSuffix(entry.Name(), ".loc")
			maps = append(maps, mapName)
		}
	}
	return maps, nil
}

func parseLoc(data []byte) ([]Location, error) {
	var locations []Location
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Format: X Y Z location_name
		// Find the first three numbers, rest is the name
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue // Skip malformed lines
		}

		x, err := strconv.ParseFloat(parts[0], 32)
		if err != nil {
			continue
		}
		y, err := strconv.ParseFloat(parts[1], 32)
		if err != nil {
			continue
		}
		z, err := strconv.ParseFloat(parts[2], 32)
		if err != nil {
			continue
		}

		// Rejoin remaining parts as the name (handles names with spaces)
		name := strings.Join(parts[3:], " ")
		name = substituteVariables(name)

		// Loc file coordinates are stored as integers * 8
		// Divide by 8 to get world coordinates
		locations = append(locations, Location{
			X:    float32(x / 8.0),
			Y:    float32(y / 8.0),
			Z:    float32(z / 8.0),
			Name: name,
		})
	}

	return locations, scanner.Err()
}

func substituteVariables(s string) string {
	result := s

	// Sort by length (longest first) to avoid partial matches
	// e.g., $loc_name_separatorlow should match before $loc_name_separator
	for varName, replacement := range locVariables {
		result = strings.ReplaceAll(result, varName, replacement)
	}

	// Clean up multiple spaces and trim
	result = strings.Join(strings.Fields(result), " ")
	return result
}
