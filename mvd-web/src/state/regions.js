// regions.js — the region-control model: which loc regions are in effect
// for the current demo, and Go's control states for them. The Locs &
// Regions tab edits it (its region editor and control matrix,
// src/tabs/locgraph/); the Timeline's Region Control panel
// (src/tabs/timeline/) reads it, and the Map's model
// (src/tabs/map/map-model.js) copies it onto the map view
// (controlRegions / locToRegion / regionToGroups / bucketStates), which the
// Map overlay and region status read.
//
// The value is null when the demo has no regions, else
//   { stage, editable, locations, defs, regions, stats, bucketStates, teamA, teamB }
// where
//   stage      'loaded'   — the demo's own regions (Go's defaults), set by
//                            loadMapAndRegions (src/app/load.js) on load and again
//                            when the deferred buckets land.
//              'resolved' — an edit's definitions resolved against the
//                            locs, the recompute still running.
//              'computed' — Go's control states for those regions.
//   editable   the editor (and the Timeline's control timeline) is shown:
//              region control needs two sides, so a
//              field of more than two individual players gets none
//              (isMultiPlayerIndividual) though Go ships regions for it.
//   locations  timelineAnalysis.locationData, what definitions resolve against
//   defs       the definitions in effect ({ name, locs } — as typed)
//   regions    them resolved ({ name, points, centroidX, centroidY })
//   stats, bucketStates, teamA, teamB — Go's answer, as it shipped it

import { store } from './store.js';
import { regionRowsFromRegions, readRegionDefs, resolveRegions, regionOverrideJSON } from '../lib/regions.js';

export const regionControl = store(null);

// resetRegionControl sets the demo's own regions (loadMapAndRegions, src/app/load.js).
export function resetRegionControl(result, editable) {
    const rc = result?.timelineAnalysis?.regionControl;
    if (!rc || !rc.regions || rc.regions.length === 0) {
        regionControl.set(null);
        return;
    }
    regionControl.set({
        stage: 'loaded',
        editable,
        locations: result.timelineAnalysis.locationData || [],
        // A hidden editor holds no rows, so a re-apply applies none.
        defs: editable ? readRegionDefs(regionRowsFromRegions(rc.regions)) : [],
        regions: rc.regions,
        stats: rc.stats || null,
        bucketStates: rc.bucketStates || null,
        teamA: rc.teamA || null,
        teamB: rc.teamB || null,
    });
}

// applyRegionDefs puts a set of definitions in effect: resolve them against
// the locs, then recompute control through the data layer (`handle`, the
// current DemoHandle — view.RegionControl in the WASM, built and shipped
// with this JS). A failed recompute is logged and leaves the resolved
// regions in place with the previous control states.
export async function applyRegionDefs(handle, defs) {
    const cur = regionControl.get();
    if (!cur) return;
    const regions = resolveRegions(defs, cur.locations);
    regionControl.set({ ...cur, stage: 'resolved', defs, regions });

    let res;
    try {
        res = JSON.parse(await handle.recomputeRegions(regionOverrideJSON(regions)));
    } catch (err) {
        console.warn('recomputeRegionControl:', err.message);
        return;
    }
    if (res.error) {
        console.warn('recomputeRegionControl:', res.error);
        return;
    }
    regionControl.set({
        ...regionControl.get(),
        stage: 'computed',
        regions,
        stats: res.stats || null,
        bucketStates: res.bucketStates || null,
        teamA: res.teamA,
        teamB: res.teamB,
    });
}

// reapplyRegions applies the definitions in effect again — the Map's model
// calls it when the map geometry arrives, so the overlay's region → loc-group lookup
// is rebuilt against the new groups.
export function reapplyRegions(handle) {
    const cur = regionControl.get();
    if (cur) return applyRegionDefs(handle, cur.defs);
}
