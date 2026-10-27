// accuracy.js — the per-weapon accuracy cell of Summary's Weapon Stats and
// the hit-count conventions behind its `≠` mark (README "The ≠ on an Acc
// cell"). getAccuracyClass and RECON_ACCURACY_NOTE are shared with the Aim
// Stats tab (src/lib/aim.js).

import { escapeHtml } from './html.js';

// The six weapons the Weapon Stats and Item Pickups tables have columns for.
export const HOLD_WEAPON_NAMES = ['sg', 'ssg', 'sng', 'gl', 'rl', 'lg'];

// What a hits count counts, keyed by playerStats `hitsConvention`. The same
// weapon's percentage is on a different scale under each, which is why the
// cell says so instead of leaving the reader to compare a KTX shotgun figure
// with a fire-counted one six times smaller.
export const HITS_CONVENTION_TITLES = {
    anyDamage: 'Hits = fires that landed damage by any path (splash included) — KTX\'s own convention for LG, NG/SNG and the axe',
    directImpact: 'Hits = projectiles that touched a player (KTX\'s own RL/GL convention) — reads far lower than an any-path count',
    pellets: 'Hits and attacks are PELLET counts (6 per SG fire, 14 per SSG), KTX\'s own unit for the shotguns',
};

// What KTX's OWN hits counter counts for one weapon. The mirror of
// view.ktxHitsConvention (mvd-analytics/view/player_stats.go), the function
// that stamps `hitsConvention` on the KTX-overlaid rows this UI renders — the
// WASM entry point applies that overlay (cmd/wasm/main.go), so both scales
// reach this table, one demo at a time.
//
// The block is not uniform, which is why the marker below is per-weapon:
// sg/ssg advance per PELLET on both sides of the ratio, rl/gl only when the
// projectile touches a player, and everything else once per fire that
// damaged — the same event as a direct hit for a weapon with one damage path.
export function ktxHitsConvention(weapon) {
    if (weapon === 'sg' || weapon === 'ssg') return 'pellets';
    if (weapon === 'rl' || weapon === 'gl') return 'directImpact';
    return 'anyDamage';
}

// The mark an Acc cell wears when its convention is not the one the server's
// own counter uses for that weapon — so a reader who has seen this weapon's
// number on a scoreboard, on the hub or in a screenshot of another demo is
// looking at a different quantity here, not a better or worse player. It is
// in the TEXT rather than only in the tooltip for the reason the "~" on a
// hedged date is: this is a figure people screenshot.
//
// A KTX-overlaid family (every demo with a demoinfo block) matches by
// construction and wears nothing. Since schema v75 both computed tiers match
// on every weapon but one, and that one is a place where the tier's evidence
// genuinely cannot answer KTX's question, not a place where it declined to:
//
//   - SG/SSG on a RECONSTRUCTED row. The recon tier counts fires, not
//     pellets: a reconstructed delta merges every hit landing on one instant,
//     so dividing its magnitude would credit one shooter with another's
//     pellets. (Its rl/gl publish KTX's direct-impact count, schema v74.)
//
//   - RL/GL on a DERIVED row whose direct/splash split never ran — a payload
//     parsed without the spatial projectile streams (gl, whose touch
//     classifier reads them) or one where the linker resolved no rl/gl fire
//     at all. The row falls back to the any-path count, which is a DIFFERENT
//     quantity from KTX's touch count and about 4x larger on rl.
//
// So the mark's explanation is per weapon, not one sentence: what the
// server's own counter counts differs between the two families above, and a
// note that named only pellets would be false on every rl/gl cell wearing it.
//
// GL on a DERIVED (wire-linked) row wore the mark until v75 and no longer
// does. The wire log genuinely holds no record of a grenade touch — the touch
// detonates the grenade and every row the server then writes is flagged splash
// — so that count is not read off the wire at all: it is re-derived from the
// flight geometry and the 2.5 s fuse by the same classifier a reconstructed
// row uses (92.0% of 424 archive player rows exact against the verbatim KTX
// block). This UI's parse always builds the projectile streams that classifier
// reads, so a gl cell here is always on KTX's scale; a payload from a parse
// without them says so in `hitsConvention` and is marked like any other cell.
export const ACC_OFF_SCALE_MARK = '≠';
// What one hit COUNTS under each convention, in the reader's terms. Both
// halves of the mark's sentence are looked up here — ours from the row's own
// `hitsConvention`, the server's from ktxHitsConvention(weapon) — so the note
// states the actual disagreement for the actual weapon instead of naming one
// family's.
const HITS_CONVENTION_PHRASES = {
    anyDamage: 'any fire that landed damage, splash included',
    directImpact: 'only a projectile that TOUCHED a player',
    pellets: 'a PELLET (6 per SG fire, 14 per SSG), on both sides of the ratio',
};
export function accOffScaleNote(entry, wn) {
    const ours = HITS_CONVENTION_PHRASES[entry.hitsConvention];
    const theirs = HITS_CONVENTION_PHRASES[ktxHitsConvention(wn)];
    const what = (ours && theirs) ? `a hit here is ${ours}, where KTX counts ${theirs}. ` : '';
    return 'Not on the server\'s own scale for this weapon: ' + what +
        'Comparable with another marked figure for the same weapon — not with a KTX scoreboard\'s.';
}
// The same statement as the table's footnote, which is where a reader who
// cannot hover — anyone looking at a screenshot of this panel — meets it. One
// line stands under a table of several weapons, so it names both scales the
// mark can be measured against rather than one, and sends the reader to the
// cell for which of them applies.
export const ACC_OFF_SCALE_FOOTNOTE = `${ACC_OFF_SCALE_MARK} — counted on a different scale from the server's own for that weapon ` +
    '(hover the cell for which): KTX counts PELLETS on SG/SSG and only projectiles that TOUCHED a player on RL/GL, ' +
    'where a marked figure counts fires that landed damage by any path. ' +
    'Two accuracies are comparable when the weapon AND the convention match, so a marked figure and a KTX scoreboard\'s are not.';

// Shared by the Summary accuracy cells and the Aim tab's recovered Hits: what
// a reconstructed hit count is, and the one caveat that comes with it.
export const RECON_ACCURACY_NOTE = 'Hits RECONSTRUCTED — this demo carries no wire damage log, ' +
    'so the hits were recovered from the rebuilt one. As complete as the damage evidence (see the note above the table).';

// The colour band of an accuracy percentage. Inert on the Summary cells
// today (README: the only rule for these classes is scoped to an id no
// longer in the DOM); anyone re-pointing it must scope it by convention.
export function getAccuracyClass(acc) {
    if (acc >= 40) return 'accuracy-high';
    if (acc >= 25) return 'accuracy-medium';
    return 'accuracy-low';
}

// Accuracy cell from a playerStats accuracy family. `hits` is ABSENT (not
// zero) when neither the wire nor the reconstruction could link fires to
// damage, so an entry with attacks but no hits renders the attack count alone
// rather than a fabricated 0%.
//
// `src` is the family's evidence grade and is taken from the family, not the
// weapon row: `ktx` is the server's own block, `derived` our own count off a
// wire damage stream, and `reconstructed` (schema v74) a figure recovered
// from the rebuilt damage log on a demo that carries no such stream. The last
// is MARKED — a recon accuracy is only as complete as damage.coverage, which
// the panel banner above the table states — while the first two, both
// measured off the wire, render plainly. Grade is not the same question as
// what the number counts: since schema v75 all three sources answer KTX's
// question on most weapons (the two that still cannot are marked, below), and
// the convention rides the tooltip either way — two accuracies are comparable
// exactly when weapon and hitsConvention match.
//
// An entry with attacks and no hits is a WITHHELD count, not a zero: nailgun
// rows carry one whenever the demo was parsed without nail decoding, which is
// the only way ng/sng fires can be linked to their damage.
export function formatAccuracyCell(accuracy, wn) {
    const entry = accuracy?.byWeapon?.[wn];
    if (!entry || !entry.attacks) return '-';
    if (entry.hits == null) return `<span class="stat-muted" title="No linked damage to count hits against — attacks only">${entry.attacks} atk</span>`;
    const pct = ((entry.hits / entry.attacks) * 100).toFixed(1);
    const notes = [];
    if (HITS_CONVENTION_TITLES[entry.hitsConvention]) notes.push(HITS_CONVENTION_TITLES[entry.hitsConvention]);
    const offScale = accOffKtxScale(entry, wn);
    if (offScale) notes.push(accOffScaleNote(entry, wn));
    const recon = accuracy.src === 'reconstructed';
    if (recon) notes.push(RECON_ACCURACY_NOTE);
    const attr = notes.length ? ` title="${escapeHtml(notes.join(' · '))}"` : '';
    const mark = offScale ? `<sup class="acc-off-scale">${ACC_OFF_SCALE_MARK}</sup>` : '';
    const cell = `<span class="${getAccuracyClass(parseFloat(pct))}"${attr}>${pct}%${mark}</span>`;
    return recon ? `<span class="stat-recon">${cell}</span>` : cell;
}

// One Acc entry against the server's own scale for its weapon. A row with no
// published convention is not marked: the field is present whenever `hits` is
// (RESULT_SCHEMA, PlayerStatsAcc), so its absence means there is no number to
// put on a scale, not that the scale agrees.
export function accOffKtxScale(entry, wn) {
    return !!entry.hitsConvention && entry.hitsConvention !== ktxHitsConvention(wn);
}

// Whether any RENDERED cell wears the mark, which decides whether the
// footnote explaining it appears. Same rows, same weapon list and same
// has-a-number test the cells use, so the note cannot appear over an unmarked
// table (an axe or nailgun entry the table does not show) or go missing under
// a marked one.
export function anyAccOffKtxScale(rows) {
    return rows.some(r => HOLD_WEAPON_NAMES.some(wn => {
        const e = r.accuracy?.byWeapon?.[wn];
        return !!e && !!e.attacks && e.hits != null && accOffKtxScale(e, wn);
    }));
}
