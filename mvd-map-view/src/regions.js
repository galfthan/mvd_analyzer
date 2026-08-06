// Region-control decoding. The Go analyzer emits one character per bucket
// (mvd-analytics/analyzer/region_control.go); this is the reverse mapping to
// the state names the overlay and the region timeline draw with.

export const REGION_STATE_BY_CHAR = {
    '_': 'empty',
    'A': 'teamAControl', 'a': 'teamAWeakControl',
    'B': 'teamBControl', 'b': 'teamBWeakControl',
    'C': 'contested',    'c': 'weakContested',
};

export function decodeRegionStateChar(c) {
    return REGION_STATE_BY_CHAR[c] || 'empty';
}
