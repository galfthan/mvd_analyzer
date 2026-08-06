// Small shared primitives. Kept separate so geometry.js and the time-indexed
// lookups can both use them without a cycle.

// lowerBoundIndex: index of the last entry whose accessor value is <= t, or
// -1 when every entry is later than t. The accessor takes (array, index) so
// callers can search columnar arrays without materialising objects.
export function lowerBoundIndex(arr, t, accessor) {
    let lo = 0, hi = arr.length - 1;
    if (hi < 0 || accessor(arr, 0) > t) return -1;
    while (lo < hi) {
        const mid = (lo + hi + 1) >> 1;
        if (accessor(arr, mid) <= t) lo = mid;
        else hi = mid - 1;
    }
    return lo;
}

// trailIndexAtTime: position within a trail point list ([{t, …}, …]) at time.
export function trailIndexAtTime(points, time) {
    return lowerBoundIndex(points, time, (a, i) => a[i].t);
}
