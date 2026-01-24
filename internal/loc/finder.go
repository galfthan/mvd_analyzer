package loc

import "math"

// FindNearest returns the name of the closest location to the given coordinates
// Returns empty string if no locations are loaded
func (f *Finder) FindNearest(x, y, z float32) string {
	if len(f.locations) == 0 {
		return ""
	}

	minDistSq := float32(math.MaxFloat32)
	var nearest string

	for _, loc := range f.locations {
		dx := x - loc.X
		dy := y - loc.Y
		dz := z - loc.Z
		distSq := dx*dx + dy*dy + dz*dz

		if distSq < minDistSq {
			minDistSq = distSq
			nearest = loc.Name
		}
	}

	return nearest
}

// FindNearestWithDistance returns the name and distance to the closest location
// Returns empty string and 0 if no locations are loaded
func (f *Finder) FindNearestWithDistance(x, y, z float32) (string, float32) {
	if len(f.locations) == 0 {
		return "", 0
	}

	minDistSq := float32(math.MaxFloat32)
	var nearest string

	for _, loc := range f.locations {
		dx := x - loc.X
		dy := y - loc.Y
		dz := z - loc.Z
		distSq := dx*dx + dy*dy + dz*dz

		if distSq < minDistSq {
			minDistSq = distSq
			nearest = loc.Name
		}
	}

	return nearest, float32(math.Sqrt(float64(minDistSq)))
}

// Locations returns all locations in the finder
func (f *Finder) Locations() []Location {
	return f.locations
}

// FindLocationsInRadius returns all locations within the given radius of the point
func (f *Finder) FindLocationsInRadius(x, y, z, radius float32) []Location {
	if len(f.locations) == 0 {
		return nil
	}

	radiusSq := radius * radius
	var result []Location

	for _, loc := range f.locations {
		dx := x - loc.X
		dy := y - loc.Y
		dz := z - loc.Z
		distSq := dx*dx + dy*dy + dz*dz

		if distSq <= radiusSq {
			result = append(result, loc)
		}
	}

	return result
}
