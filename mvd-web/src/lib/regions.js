// regions.js — region-control definitions as data: the editor's rows, the
// definitions read from them, their resolution against the demo's locs, the
// derived lookups the Map draws with, and the Save… / Load… JSON. DOM-free;
// the model in effect is src/state/regions.js.
//
// A definition is { name, locs: [loc name…] }; a resolved region is
// { name, points: [{x,y,z,name}], centroidX, centroidY } — the shape Go
// ships in timelineAnalysis.regionControl.regions.

// The editor's rows for a set of resolved regions: one per region, its
// distinct loc names comma-joined.
export function regionRowsFromRegions(regions) {
    return regions.map((region) => ({
        name: region.name,
        locs: [...new Set(region.points.map(p => p.name))].join(', '),
    }));
}

// readRegionDefs reads the definitions out of the editor rows, in row order
// so the user's region order is preserved when serialised. Empty rows (no
// name or no locs) are dropped silently — they're transient editing state.
export function readRegionDefs(rows) {
    const out = [];
    for (const row of rows) {
        const name = (row.name || '').trim();
        const locs = (row.locs || '').split(',').map(s => s.trim()).filter(s => s);
        if (!name || locs.length === 0) continue;
        out.push({ name, locs });
    }
    return out;
}

// resolveRegions matches definitions against the demo's locs. Duplicate
// names are dropped — first occurrence wins so editing a row above doesn't
// get masked by a stale row below sharing the same name. The loc match is
// case-insensitive so a hand-edited "ya" still claims the canonical "YA"
// loc — saves the user having to memorize the exact post-substitution
// casing. A definition that matches no loc is dropped.
export function resolveRegions(defs, locations) {
    const regions = [];
    const seenNames = new Set();
    for (const def of defs) {
        if (seenNames.has(def.name)) continue;
        seenNames.add(def.name);

        const locSet = new Set(def.locs.map(s => s.toLowerCase()));
        const points = [];
        let sumX = 0, sumY = 0;
        for (const loc of locations) {
            if (locSet.has(loc.name.toLowerCase())) {
                points.push({ x: loc.x, y: loc.y, z: loc.z, name: loc.name });
                sumX += loc.x;
                sumY += loc.y;
            }
        }
        if (points.length > 0) {
            regions.push({
                name: def.name,
                points: points,
                centroidX: sumX / points.length,
                centroidY: sumY / points.length,
            });
        }
    }
    return regions;
}

// Loc name → region name.
export function locToRegionOf(regions) {
    const out = {};
    for (const region of regions) {
        for (const pt of region.points) out[pt.name] = region.name;
    }
    return out;
}

// Region name → the map's loc groups it covers (for the control overlay's
// colouring): a group belongs to a region when one of its points sits on
// one of the region's points in XY.
export function regionToGroupsOf(regions, groups) {
    const out = {};
    if (!groups) return out;
    for (const group of groups) {
        for (const region of regions) {
            let matched = false;
            for (const gpt of group.points) {
                for (const rpt of region.points) {
                    const dx = gpt.x - rpt.x, dy = gpt.y - rpt.y;
                    if (dx * dx + dy * dy < 1) {
                        matched = true;
                        break;
                    }
                }
                if (matched) break;
            }
            if (matched) {
                if (!out[region.name]) out[region.name] = [];
                out[region.name].push(group);
            }
        }
    }
    return out;
}

// The override the WASM recompute (view.RegionControl) takes.
export function regionOverrideJSON(regions) {
    return JSON.stringify({
        regions: regions.map(r => ({
            name: r.name,
            locs: r.points.map(p => p.name),
        })),
    });
}

// Save…: the definitions as typed (not as resolved), and the file name —
// the map name as a safe stem, "regions" when no map info is available.
export function regionFileJSON(defs) {
    return JSON.stringify({ regions: defs }, null, 4) + '\n';
}

export function regionFileName(mapKey) {
    const base = String(mapKey || '').replace(/[^a-z0-9_-]/g, '');
    return (base || 'regions') + '.json';
}

// Load…: the editor rows for a region file, or { error } for the alert.
// A region without a name is skipped; `locs` is optional.
export function parseRegionFile(text) {
    let parsed;
    try {
        parsed = JSON.parse(text);
    } catch (err) {
        return { error: 'Failed to parse region JSON: ' + err.message };
    }
    const regions = Array.isArray(parsed?.regions) ? parsed.regions : null;
    if (!regions) return { error: 'Region JSON must have a top-level "regions" array.' };
    const rows = [];
    for (const r of regions) {
        const name = String(r.name || '').trim();
        const locs = Array.isArray(r.locs) ? r.locs.map(s => String(s)) : [];
        if (!name) continue;
        rows.push({ name, locs: locs.join(', ') });
    }
    return { rows };
}
