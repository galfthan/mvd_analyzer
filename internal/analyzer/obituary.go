package analyzer

import "strings"

// obituaryWeapon describes one weapon family that can attribute a kill in a
// QuakeWorld obituary line. Each entry lists the trailing forms that the
// server prints; extractKillerName walks these in order and matches the
// first one that lands inside the input.
//
// Quad variants must be scanned before their non-quad equivalents at the
// table level (see killerSuffixes below) so that "X's quad rocket" never
// matches "X's quad" + " rocket" by accident.
type obituaryWeapon struct {
	weapon       string   // canonical short code: "rl", "lg", "ssg", ...
	suffixes     []string // non-quad tails
	quadSuffixes []string // quad tails (assigned to the same weapon)
}

// obituaryWeapons is the single source of truth for which obituary suffix
// belongs to which weapon. Both the FragAnalyzer and the MessagesAnalyzer
// use this table; previously each had its own near-duplicate list.
//
// (The weapon attribution itself isn't consumed by extractKillerName today —
// that information lives in the per-pattern killPatterns table in
// messages.go and frag.go — but keeping the suffix list grouped by weapon
// makes it obvious where to add a new variant when one shows up in a demo.)
var obituaryWeapons = []obituaryWeapon{
	{weapon: "rl", suffixes: []string{"'s rocket", "'s pineapple", "' rocket"}, quadSuffixes: []string{"'s quad rocket", "'s quad pineapple"}},
	{weapon: "lg", suffixes: []string{"'s shaft", "'s lightning", "'s discharge", "' discharge"}, quadSuffixes: []string{"'s quad shaft", "'s quad lightning"}},
	{weapon: "gl", suffixes: []string{"'s grenade", "' grenade"}, quadSuffixes: []string{"'s quad grenade"}},
	{weapon: "ssg", suffixes: []string{"'s boomstick", "'s buckshot", "' buckshot"}, quadSuffixes: []string{"'s quad boomstick"}},
	{weapon: "axe", suffixes: []string{"'s axe"}, quadSuffixes: []string{"'s quad axe"}},
	{weapon: "ng", suffixes: []string{"'s batteries"}},
	{weapon: "fall", suffixes: []string{"'s fall", "' fall"}},
}

// killerSuffixes is the flat suffix list extracted from obituaryWeapons in
// the order extractKillerName needs to scan them: every quad variant first
// (so "X's quad rocket" matches before "X's rocket" can), then every
// non-quad variant.
var killerSuffixes = buildKillerSuffixes()

func buildKillerSuffixes() []string {
	var out []string
	for _, w := range obituaryWeapons {
		out = append(out, w.quadSuffixes...)
	}
	for _, w := range obituaryWeapons {
		out = append(out, w.suffixes...)
	}
	return out
}

// quadOnlySuffixes are the quad-specific tail strings used by stripQuadSuffix
// to peel quad annotation off names that have already been extracted by some
// other means (e.g. the "rockets from <name>" path in the frag parser). The
// generic "'s quad" sentinel comes last so it only fires when no longer
// variant matched.
var quadOnlySuffixes = buildQuadOnlySuffixes()

func buildQuadOnlySuffixes() []string {
	var out []string
	for _, w := range obituaryWeapons {
		out = append(out, w.quadSuffixes...)
	}
	out = append(out, "'s quad")
	return out
}

// extractKillerName trims a known weapon suffix off the tail of an obituary
// fragment and returns the killer's display name.
//
// Note: we deliberately do NOT strip a trailing '.' as punctuation. QuakeWorld
// names can legitimately end in '.' — see the demo broken.mvd.gz, which has a
// player named ".N3ophyt3." after Q_normalizetext folding. Stripping the dot
// splits that player's frags off into a phantom name that the frontend can't
// reconcile.
func extractKillerName(rest string) string {
	for _, suffix := range killerSuffixes {
		if idx := strings.Index(rest, suffix); idx > 0 {
			return strings.TrimSpace(rest[:idx])
		}
	}

	// "<count> rockets from <killer>" pattern: rare splash-damage attribution.
	if idx := strings.Index(rest, " rockets from "); idx >= 0 {
		killer := strings.TrimSpace(rest[idx+len(" rockets from "):])
		return stripQuadSuffix(killer)
	}

	rest = strings.TrimSuffix(rest, "\n")
	return strings.TrimSpace(stripQuadSuffix(rest))
}

// stripQuadSuffix removes a trailing quad-annotation from a name that's
// already been extracted from somewhere upstream.
func stripQuadSuffix(name string) string {
	for _, suffix := range quadOnlySuffixes {
		name = strings.TrimSuffix(name, suffix)
	}
	return strings.TrimSpace(name)
}
