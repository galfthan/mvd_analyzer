// provenance.js — the damage-evidence banner (damage.source /
// damage.coverage, v74) shared by the Summary (Basic and Weapon Stats), Key
// Moments and Aim Stats tabs.
//
// Everything that rides the reconstructed damage section — the Basic Stats
// damage columns, Weapon Stats' Dmg and Acc, the Aim tab's Dmg and its recon
// hits, Key Moments' damage-ranked tables — inherits its evidence grade from
// this one field rather than carrying a copy, so one renderer states it
// above each of those tables.
//
// `coverage.ratio` is published as a MAGNITUDE, not a flag: the pipeline has
// no threshold and neither does this. The number is printed as it comes; the
// only comparison here is against 1, which is the definition of a complete
// section rather than a cutoff picked by the UI.
//
// Returns { html, warn } or null when the section is measured.
/** @typedef {{ html: string, warn: boolean }} ProvenanceNote */

/**
 * @param {import('./result.js').Result | null | undefined} result
 * @returns {ProvenanceNote | null}
 */
export function damageProvenanceHtml(result) {
    if (result?.damage?.source !== 'reconstructed') return null;
    const lead = 'Damage on this demo is <strong>reconstructed</strong>: it predates the server\'s damage log, ' +
        'so every damage figure below is rebuilt from the health/armor streams rather than measured.';
    const cov = result.damage.coverage;
    // Absent coverage on a reconstructed section means the frag log named no
    // scoreable kill, so completeness was never assessed — a different
    // statement from "complete", and silence would read as the latter.
    if (!cov) {
        return { html: `${lead} The frag log named no scoreable kill, so how much of the match the evidence covers was not assessed.`, warn: false };
    }
    if (cov.ratio >= 1) {
        return { html: `${lead} The evidence accounts for all ${cov.kills} of the match's scoreable kills.`, warn: false };
    }
    return {
        html: `${lead} <strong>Damage evidence covers ${coveragePct(cov.ratio)}% of this match's kills</strong> ` +
            `(${cov.covered} of ${cov.kills} scoreable kills) — the figures below are floors, not measurements.`,
        warn: true,
    };
}

// The coverage percentage for the INCOMPLETE banner. Rounding only — one
// decimal below 10% so a 5.1% reading does not collapse to "5%" — with one
// hard rule: the printed figure must never reach 100 while the ratio is below
// it. A ratio in [0.995, 1) rounds up to a bold "100%" inside the sentence
// that exists to say the section is incomplete. That window opens on the big
// team games — one uncovered kill out of 200+ — and those are real: 10 of the
// 60 reconstructed demos in a 120-demo archive sample carry 200 or more
// scoreable kills, the largest 843. There it truncates to two decimals
// instead, which cannot reach 100 from below.
/** @param {number} ratio */
export function coveragePct(ratio) {
    const pct = ratio * 100;
    const rounded = pct.toFixed(ratio < 0.1 ? 1 : 0);
    if (parseFloat(rounded) < 100) return rounded;
    return (Math.floor(pct * 100) / 100).toFixed(2);
}

// The banner's class: a warning when the evidence is incomplete.
/** @param {ProvenanceNote | null | undefined} note */
export function provenanceClass(note) {
    return note && note.warn ? 'panel-provenance warn' : 'panel-provenance';
}
