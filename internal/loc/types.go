package loc

// Location represents a named point in a Quake map
type Location struct {
	X, Y, Z float32 // World coordinates (loc file coords divided by 8)
	Name    string  // Human-readable location name (variables substituted)
}

// Finder provides efficient nearest-location lookup for a map
type Finder struct {
	mapName   string
	locations []Location
}

// MapName returns the name of the map this finder is for
func (f *Finder) MapName() string {
	return f.mapName
}

// LocationCount returns the number of locations loaded
func (f *Finder) LocationCount() int {
	return len(f.locations)
}
