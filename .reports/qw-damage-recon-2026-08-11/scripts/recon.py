#!/usr/bin/env python3
"""Reconstruct per-hit bounded damage events from QW MVD state streams
(h/a deltas + beams/projectiles/shots/positions), without reading damage.events.

Produces a list of events shaped like result.DamageEntry:
  {time, attacker, victim, weapon, bounded, isSelf, isTeam, isEnv, victimWep}
plus a topKills-equivalent burst assembly (killBurstFor parity).
"""
import bisect
import json
import math
from collections import defaultdict

# ---------- helpers ----------

class Track:
    """Piecewise-linear interpolated position track."""
    def __init__(self, pos):
        self.t = pos["t"]
        self.x = pos["x"]; self.y = pos["y"]; self.z = pos["z"]
        self.vx = pos.get("vx"); self.vy = pos.get("vy"); self.vz = pos.get("vz")
        self.vp = pos.get("vp"); self.vya = pos.get("vya")

    def vel_delta(self, t, pre_ms=40, post_ms=60):
        """Velocity change across the instant t (knockback signal)."""
        if not self.vx:
            return None
        i0 = bisect.bisect_left(self.t, t - pre_ms) - 1
        i1 = bisect.bisect_right(self.t, t + post_ms)
        if i0 < 0 or i1 >= len(self.t):
            return None
        return (self.vx[i1] - self.vx[i0], self.vy[i1] - self.vy[i0],
                self.vz[i1] - self.vz[i0])

    def aim_at(self, t):
        """(pitch, yaw) degrees at nearest sample. Raw angle16 decoded;
        pitch wrapped to signed (-180, 180], positive = down."""
        if not self.vya:
            return None
        i = bisect.bisect_right(self.t, t) - 1
        i = max(0, min(i, len(self.t) - 1))
        yaw = (self.vya[i] & 0xFFFF) * 360.0 / 65536.0
        pitch = ((self.vp[i] & 0xFFFF) * 360.0 / 65536.0) if self.vp else 0.0
        if pitch > 180.0:
            pitch -= 360.0
        return (pitch, yaw)

    def aim_angle_to(self, t, target):
        """Angle (deg) between the view direction at t and the eye->target ray."""
        aim = self.aim_at(t)
        if aim is None:
            return None
        pitch, yaw = aim
        eye = self.at(t)
        dx, dy, dz = target[0]-eye[0], target[1]-eye[1], (target[2]+4)-(eye[2]+22)
        L = math.sqrt(dx*dx+dy*dy+dz*dz)
        if L < 1:
            return 0.0
        p = math.radians(pitch); y = math.radians(yaw)
        fw = (math.cos(p)*math.cos(y), math.cos(p)*math.sin(y), -math.sin(p))
        c = (fw[0]*dx+fw[1]*dy+fw[2]*dz)/L
        return math.degrees(math.acos(max(-1.0, min(1.0, c))))

    def at(self, t):
        ts = self.t
        i = bisect.bisect_right(ts, t) - 1
        if i < 0:
            return (self.x[0], self.y[0], self.z[0])
        if i >= len(ts) - 1:
            return (self.x[-1], self.y[-1], self.z[-1])
        t0, t1 = ts[i], ts[i+1]
        f = 0.0 if t1 == t0 else (t - t0) / (t1 - t0)
        dx = self.x[i+1]-self.x[i]; dy = self.y[i+1]-self.y[i]; dz = self.z[i+1]-self.z[i]
        if dx*dx+dy*dy+dz*dz > 400*400:  # teleport/respawn: don't interpolate
            return (self.x[i], self.y[i], self.z[i])
        return (self.x[i]+f*dx, self.y[i]+f*dy, self.z[i]+f*dz)

def dist(p, q):
    return math.sqrt((p[0]-q[0])**2 + (p[1]-q[1])**2 + (p[2]-q[2])**2)

def seg_dist(p, a, b):
    """Distance from point p to segment a-b."""
    abx, aby, abz = b[0]-a[0], b[1]-a[1], b[2]-a[2]
    apx, apy, apz = p[0]-a[0], p[1]-a[1], p[2]-a[2]
    ab2 = abx*abx + aby*aby + abz*abz
    t = 0.0 if ab2 == 0 else max(0.0, min(1.0, (apx*abx+apy*aby+apz*abz)/ab2))
    cx, cy, cz = a[0]+t*abx, a[1]+t*aby, a[2]+t*abz
    return math.sqrt((p[0]-cx)**2 + (p[1]-cy)**2 + (p[2]-cz)**2)

def in_interval(intervals, t):
    for iv in intervals or []:
        if iv["s"] <= t < iv["e"]:
            return True
    return False

def load(path):
    return json.load(open(path))

# ---------- step 1: per-victim bounded deltas ----------

def victim_deltas(d):
    """Per-player list of {t, dh, da, bounded, died, h_before} drops,
    excluding mega-rot, pickups, spawn resets."""
    out = {}
    for p in d["streams"]["players"]:
        name = p["name"]
        spawns = set(p.get("sp") or [])
        merged = defaultdict(dict)
        for c in p.get("h") or []:
            merged[c["t"]]["h"] = c["v"]
        for c in p.get("a") or []:
            merged[c["t"]]["a"] = c["v"]
        times = sorted(merged)
        events = []
        ph = pa = None
        for t in times:
            nh = merged[t].get("h", ph)
            na = merged[t].get("a", pa)
            if ph is None:
                ph, pa = nh, (na if na is not None else 0)
                continue
            if na is None:
                na = pa
            if t in spawns:
                ph, pa = nh, na
                continue
            dh = ph - nh if nh < ph else 0
            da = pa - na if na < pa else 0
            if dh == 1 and ph > 100 and da == 0:  # mega rot tick
                ph, pa = nh, na
                continue
            if dh > 0 or da > 0:
                died = nh <= 0 < ph
                if ph <= 0:
                    share = 0          # corpse hit
                elif nh <= 0:
                    share = ph         # killing hit: cap at remaining health
                else:
                    share = dh
                bounded = share + (da if ph > 0 else 0)
                if bounded > 0:
                    events.append({"t": t, "dh": dh, "da": da, "bounded": bounded,
                                   "died": died, "h_before": ph})
            ph, pa = nh, na
        out[name] = events
    return out

# ---------- step 2: attribution ----------

DEFAULTS = dict(
    TOL_BEAM=30,       # ms beam-flash to damage-frame (measured: 0)
    TOL_PROJ=130,      # ms projectile-end to damage-frame (measured p5=-81, p99=261)
    TOL_SHOT=60,       # ms hitscan sound to damage-frame
    R_BEAM_SEG=90.0,   # units victim to beam segment (p99 measured 60, max 79)
    R_BEAM_SRC=160.0,  # units beam start to attacker eye
    R_PROJ_SRC=220.0,  # units projectile spawn to shooter (p50=81, p90=152)
    R_SPLASH=380.0,    # units projectile end to victim (p95=199)
    R_DIRECT=56.0,
    NAIL_SPEED=1000.0, # nails AND rockets fly 1000 ups
    ROCKET_SPEED=1000.0,
    TOL_FLIGHT=180.0,  # ms flight-time consistency for sound-based projectile fallback
    R_AXE=110.0,
    R_HITSCAN=3000.0,
    # Additive score penalty on self candidates — an enemy-attribution prior.
    # None = auto by mode: 0.6 in duels (2 players), 0.1 in team games.
    # Rationale (measured, fresh-sample confirmed): in the ambiguous close-range
    # mutual-rocket frame, preferring the enemy explanation converts silent
    # juicy-burst recall losses into rare slight inflations. Duel juicy-RL
    # recall 93.5%->98.7% for a 98.6%->96.2% precision trade; effect saturates
    # at 0.6 (beyond that self only wins when it is the sole explanation).
    SELF_PEN=None,
)

POSITIONAL_KILLS = ("tele", "stomp", "squish")

MID_WEAPONS = ("gl", "ssg", "sng", "ng")

def damage_model_score(obs, died, weapon, kind, d_end, is_self, quad):
    """Score a candidate by how well the observed bounded delta matches the QW
    damage model. bounded == raw for every surviving hit, so magnitude is a
    sharp discriminator. Returns a penalty >= 0 (lower is better), or None if
    physically infeasible.

    QW radius damage: received = D - 0.5*dist(center, explosion), the
    attacker's own splash = D - 0.25*dist. Rocket D = 100..120, grenade 120.
    LG = 30/cell, sg pellets 4 dmg x6, ssg x14, ng spike 9, sng 18, axe 20.
    Quad multiplies x4.
    """
    q = 4.0 if quad else 1.0
    lo = hi = None
    if kind in ("beam",):
        # 30/cell, up to 2 cells per server frame; lo stretched down to the
        # armor-only share (ra 24 / ya 18 / ga 9) seen when a server mode
        # nullifies the health share (povdmm4-style)
        lo, hi = 9.0 * q, 60.0 * q
    elif kind in ("proj", "rl-sound"):
        D_lo, D_hi = (100.0, 120.0) if weapon == "rl" else (120.0, 120.0)
        if kind == "rl-sound" or d_end is None or d_end < 48.0:
            # direct hit (or unknown explosion point): full damage possible;
            # a direct can also stack same-frame splash from its own explosion? no
            lo, hi = D_lo * q, D_hi * q
            if kind == "rl-sound":
                lo = 25.0 * q  # unknown geometry: splash values allowed, but a
                               # tiny delta is far likelier another cause
        else:
            f = 0.25 if is_self else 0.5
            lo = (D_lo - f * (d_end + 60.0)) * q   # slack for interp error
            hi = (D_hi - f * max(0.0, d_end - 60.0)) * q
            if hi <= 0:
                return None
            lo = max(1.0, lo)
    elif kind == "hitscan":
        if weapon == "sg":
            lo, hi = 4.0 * q, 24.0 * q
        elif weapon == "ssg":
            lo, hi = 4.0 * q, 56.0 * q
        else:  # axe
            lo = hi = 20.0 * q
    elif kind == "nail":
        per = 9.0 if weapon == "ng" else 18.0
        lo, hi = per * q, per * q * 3  # up to a few spikes per frame
    else:
        return 0.0
    if died:
        # killing hit: bounded is capped below raw; obs below range is fine
        lo = 1.0
    if obs < lo - 0.5:
        pen = (lo - obs)
    elif obs > hi + 0.5:
        pen = (obs - hi)
    else:
        return 0.0
    scale = max(10.0, 0.25 * hi)
    return pen / scale

def victim_wep(p, t):
    has_rl = in_interval(p.get("rl"), t)
    has_lg = in_interval(p.get("lg"), t)
    if has_rl and has_lg: return "both"
    if has_rl: return "rl"
    if has_lg: return "lg"
    for w in MID_WEAPONS:
        if in_interval(p.get(w), t):
            return "mid"
    return "sg"

def build_recon_events(d, params=None):
    P = dict(DEFAULTS)
    if params:
        P.update(params)
    players = d["streams"]["players"]
    if P["SELF_PEN"] is None:
        mode = (d.get("demoInfo") or {}).get("mode")
        is_duel = mode == "duel" or (mode is None and len(players) == 2)
        P["SELF_PEN"] = 0.6 if is_duel else 0.1
    pbyname = {p["name"]: p for p in players}
    teams = {p["name"]: p.get("team") or p["name"] for p in players}
    tracks = {p["name"]: Track(p["pos"]) for p in players if p.get("pos")}
    frags = d["frags"]["frags"] if d.get("frags") else []
    shots = d["shots"]["shots"] if d.get("shots") else []
    beams = d["streams"].get("beams")
    projs = d["streams"].get("projectiles")

    frag_at = {}
    for f in frags:
        if f.get("isSuicide") or f.get("isTeamKill"):
            continue
        frag_at[(f["victim"], f["time"])] = f

    beam_list = []
    if beams:
        for i in range(len(beams["t"])):
            beam_list.append((beams["t"][i],
                              (beams["sx"][i], beams["sy"][i], beams["sz"][i]),
                              (beams["ex"][i], beams["ey"][i], beams["ez"][i])))
        beam_list.sort(key=lambda b: b[0])
    beam_ts = [b[0] for b in beam_list]

    proj_list = []
    if projs:
        shot_times = defaultdict(list)  # weapon -> sorted times/players
        for s in shots:
            shot_times[s["weapon"]].append((s["time"], s["player"]))
        for lst in shot_times.values():
            lst.sort()
        for i in range(len(projs["s"])):
            w = projs["w"][i]
            s_t = projs["s"][i]
            sp = (projs["sx"][i], projs["sy"][i], projs["sz"][i])
            ep = (projs["ex"][i], projs["ey"][i], projs["ez"][i])
            # candidate shooters: players who fired this weapon within 80ms
            firers = {pl for (ts, pl) in shot_times.get(w, ())
                      if abs(ts - s_t) <= 80}
            pool = firers if firers else set(tracks)
            # flight direction (exact for rockets, initial-segment approx for gl)
            fdx, fdy, fdz = ep[0]-sp[0], ep[1]-sp[1], ep[2]-sp[2]
            fl = math.sqrt(fdx*fdx + fdy*fdy + fdz*fdz)
            best, bests = None, float("inf")
            for name in pool:
                tr = tracks.get(name)
                if tr is None:
                    continue
                dd = dist(tr.at(s_t), sp)
                if dd > P["R_PROJ_SRC"]:
                    continue
                score = dd * 0.02
                if w == "rl" and fl > 60:
                    ang = None
                    aim = tr.aim_at(s_t)
                    if aim is not None:
                        p_r = math.radians(aim[0]); y_r = math.radians(aim[1])
                        fw = (math.cos(p_r)*math.cos(y_r),
                              math.cos(p_r)*math.sin(y_r), -math.sin(p_r))
                        c = (fw[0]*fdx + fw[1]*fdy + fw[2]*fdz) / fl
                        ang = math.degrees(math.acos(max(-1.0, min(1.0, c))))
                    if ang is not None:
                        if ang > 55.0:
                            continue
                        score += ang * 0.12
                if score < bests:
                    best, bests = name, score
            proj_list.append({"w": w, "s": s_t, "e": projs["e"][i],
                              "sp": sp, "ep": ep, "shooter": best})
        proj_list.sort(key=lambda p: p["e"])
    # a shot whose rocket has an entity track is accounted for: it must not
    # also act as a sound-only (trackless point-blank) candidate
    consumed = defaultdict(list)  # player -> sorted proj spawn times
    for pr in proj_list:
        if pr["w"] == "rl" and pr["shooter"] is not None:
            consumed[pr["shooter"]].append(pr["s"])
    for lst in consumed.values():
        lst.sort()

    def shot_consumed(player, ts, tol=100):
        lst = consumed.get(player)
        if not lst:
            return False
        i = bisect.bisect_left(lst, ts)
        for j in (i - 1, i):
            if 0 <= j < len(lst) and abs(lst[j] - ts) <= tol:
                return True
        return False
    proj_ts = [p["e"] for p in proj_list]

    shots_sorted = sorted(shots, key=lambda s: s["time"])
    shot_ts = [s["time"] for s in shots_sorted]
    nail_shots = [s for s in shots_sorted if s["weapon"] in ("ng", "sng")]

    deltas = victim_deltas(d)
    events = []
    for victim, dl in deltas.items():
        vtrack = tracks.get(victim)
        for ev in dl:
            t = ev["t"]
            vpos = vtrack.at(t) if vtrack else None
            f = frag_at.get((victim, t))
            if f is not None and not str(f["killer"]).startswith("world"):
                if f["weapon"] in POSITIONAL_KILLS:
                    # positional kill: no damage event in GT semantics; keep the
                    # delta out of the enemy hit index
                    events.append(mk(t, "world", victim, f["weapon"], ev,
                                     teams, pbyname, "positional"))
                else:
                    events.append(mk(t, f["killer"], victim, f["weapon"], ev,
                                     teams, pbyname, "frag-anchor"))
                continue

            cands = []  # dicts: geom, attacker, weapon, kind, d_end
            if vpos is not None:
                # LG beams: victim on segment, same frame
                lo = bisect.bisect_left(beam_ts, t - P["TOL_BEAM"])
                hi = bisect.bisect_right(beam_ts, t + P["TOL_BEAM"])
                for bi in range(lo, hi):
                    bt, bs, be = beam_list[bi]
                    sd = seg_dist(vpos, bs, be)
                    if sd > P["R_BEAM_SEG"]:
                        continue
                    best, bestd = None, P["R_BEAM_SRC"]
                    for name, tr in tracks.items():
                        if name == victim:
                            continue
                        dd = dist(tr.at(bt), bs)
                        if dd < bestd:
                            best, bestd = name, dd
                    if best:
                        cands.append({"geom": sd / P["R_BEAM_SEG"] * 0.3,
                                      "attacker": best, "weapon": "lg",
                                      "kind": "beam", "d_end": None})

                # projectiles (rl/gl): end near victim, same frame
                lo = bisect.bisect_left(proj_ts, t - P["TOL_PROJ"])
                hi = bisect.bisect_right(proj_ts, t + P["TOL_PROJ"])
                for pi in range(lo, hi):
                    pr = proj_list[pi]
                    if pr["shooter"] is None:
                        continue
                    d_end = dist(pr["ep"], vpos)
                    if d_end > P["R_SPLASH"]:
                        continue
                    cands.append({"geom": d_end / P["R_SPLASH"] * 0.5,
                                  "attacker": pr["shooter"], "weapon": pr["w"],
                                  "kind": "proj", "d_end": d_end, "ep": pr["ep"]})

                # hitscan sg/ssg + axe by sound
                lo = bisect.bisect_left(shot_ts, t - P["TOL_SHOT"])
                hi = bisect.bisect_right(shot_ts, t + P["TOL_SHOT"])
                for si in range(lo, hi):
                    s = shots_sorted[si]
                    if s["player"] == victim or s["player"] not in tracks:
                        continue
                    w = s["weapon"]
                    if w not in ("sg", "ssg", "axe"):
                        continue
                    spos = tracks[s["player"]].at(s["time"])
                    dd = dist(spos, vpos)
                    if w == "axe":
                        if dd > P["R_AXE"]:
                            continue
                        cands.append({"geom": 0.1 + dd / P["R_AXE"] * 0.2,
                                      "attacker": s["player"], "weapon": w,
                                      "kind": "hitscan", "d_end": dd})
                    else:
                        if dd > P["R_HITSCAN"]:
                            continue
                        # aim-cone gate: real sg hits are within ~25 deg (p95)
                        ang = tracks[s["player"]].aim_angle_to(s["time"], vpos)
                        if ang is not None and ang > 50.0:
                            continue
                        apen = 0.0 if ang is None else min(ang / 25.0, 2.0) * 0.25
                        cands.append({"geom": 0.15 + apen,
                                      "attacker": s["player"], "weapon": w,
                                      "kind": "hitscan", "d_end": dd})

                # nails: fired earlier, flight = dist/speed
                for s in nail_shots:
                    if s["player"] == victim or s["player"] not in tracks:
                        continue
                    dt = t - s["time"]
                    if dt < 0 or dt > 3000:
                        continue
                    spos = tracks[s["player"]].at(s["time"])
                    flight = dist(spos, vpos) / P["NAIL_SPEED"] * 1000.0
                    if abs(dt - flight) <= 150:
                        cands.append({"geom": 0.4 + abs(dt - flight) / 150 * 0.2,
                                      "attacker": s["player"], "weapon": s["weapon"],
                                      "kind": "nail", "d_end": None})

                # rocket-by-sound fallback: point-blank rockets explode before
                # the entity is ever broadcast, so no projectile track exists.
                # Restrict to SHORT flights; long flights have entity tracks.
                lo = bisect.bisect_left(shot_ts, t - 450)
                hi = bisect.bisect_right(shot_ts, t + 40)
                for si in range(lo, hi):
                    s = shots_sorted[si]
                    if s["weapon"] != "rl" or s["player"] not in tracks:
                        continue
                    if shot_consumed(s["player"], s["time"]):
                        continue
                    dt = t - s["time"]
                    if dt < -40:
                        continue
                    spos = tracks[s["player"]].at(s["time"])
                    flight = dist(spos, vpos) / P["ROCKET_SPEED"] * 1000.0
                    err = abs(dt - flight)
                    if err > P["TOL_FLIGHT"]:
                        continue
                    apen = 0.0
                    if s["player"] != victim:
                        # enemy point-blank rocket: shooter was aiming at the victim
                        ang = tracks[s["player"]].aim_angle_to(s["time"], vpos)
                        if ang is not None and ang > 60.0:
                            continue
                        apen = 0.0 if ang is None else min(ang / 30.0, 2.0) * 0.15
                    cands.append({"geom": 0.35 + err / P["TOL_FLIGHT"] * 0.25 + apen,
                                  "attacker": s["player"], "weapon": "rl",
                                  "kind": "rl-sound", "d_end": None})

            # knockback: velocity change at t points away from the explosion
            dv = vtrack.vel_delta(t) if vtrack else None
            dvn = math.sqrt(dv[0]**2 + dv[1]**2 + dv[2]**2) if dv else 0.0
            scored = []
            for c in cands:
                quad = in_interval(pbyname.get(c["attacker"], {}).get("q"), t)
                pen = damage_model_score(ev["bounded"], ev["died"], c["weapon"],
                                         c["kind"], c["d_end"],
                                         c["attacker"] == victim, quad)
                if pen is None:
                    continue
                total = c["geom"] + 1.5 * pen
                if c["attacker"] == victim:
                    total += P["SELF_PEN"]
                if c["kind"] == "proj" and dvn > 50.0 and c.get("ep") is not None:
                    ex, ey, ez = c["ep"]
                    kx, ky, kz = vpos[0]-ex, vpos[1]-ey, (vpos[2]+4)-ez
                    kl = math.sqrt(kx*kx+ky*ky+kz*kz)
                    if kl > 1:
                        cosk = (dv[0]*kx + dv[1]*ky + dv[2]*kz) / (dvn * kl)
                        total += (1.0 - cosk) * 0.3
                scored.append((total, c))
            if not scored:
                events.append(mk(t, "world", victim, "unknown", ev, teams, pbyname, "none"))
                continue
            scored.sort(key=lambda x: x[0])
            c = scored[0][1]
            events.append(mk(t, c["attacker"], victim, c["weapon"], ev, teams, pbyname, c["kind"]))

    events.sort(key=lambda e: e["time"])
    return events

def mk(t, attacker, victim, weapon, ev, teams, pbyname, kind):
    is_self = attacker == victim
    is_team = (not is_self and teams.get(attacker) is not None
               and teams.get(attacker) == teams.get(victim))
    is_env = attacker == "world"
    vw = victim_wep(pbyname[victim], t) if (not is_self and not is_team and not is_env and victim in pbyname) else ""
    return {"time": t, "attacker": attacker, "victim": victim, "weapon": weapon,
            "bounded": ev["bounded"], "isSelf": is_self, "isTeam": is_team,
            "isEnv": is_env, "victimWep": vw, "kind": kind, "died": ev["died"]}

# ---------- step 3: burst assembly (killBurstFor parity) ----------

def life_start(p, t):
    """Start of the alive interval containing t; else latest start <= t; else 0."""
    cand = 0
    for iv in p.get("alive") or []:
        if iv["s"] <= t < iv["e"]:
            return iv["s"]
        if iv["s"] <= t:
            cand = max(cand, iv["s"])
    return cand

def assemble_topkills(d, events, gap_ms=3000, contested_ms=4000):
    pbyname = {p["name"]: p for p in d["streams"]["players"]}
    frags = d["frags"]["frags"] if d.get("frags") else []
    idx = defaultdict(list)
    for e in events:
        if e["isSelf"] or e["isTeam"] or e["isEnv"]:
            continue
        idx[(e["attacker"], e["victim"])].append(e)
    for k in idx:
        idx[k].sort(key=lambda e: e["time"])

    rows = []
    for f in frags:
        if f.get("isSuicide") or f.get("isTeamKill"):
            continue
        killer, victim, t, w = f["killer"], f["victim"], f["time"], f["weapon"]
        hits = idx.get((killer, victim), [])
        vp = pbyname.get(victim)
        life = life_start(vp, t) if vp else 0
        times = [h["time"] for h in hits]
        hi = bisect.bisect_right(times, t)
        b_damage = b_hits = 0
        maxgap = 0
        earliest = None
        victimw = ""
        anchored = False
        for i in range(hi - 1, -1, -1):
            e = hits[i]
            if e["time"] < life:
                break
            if e["weapon"].lower() != w.lower():
                continue
            if not anchored:
                if e["time"] != t:
                    break
                anchored = True
                earliest = t
            else:
                gap = earliest - e["time"]
                if gap > gap_ms:
                    break
                maxgap = max(maxgap, gap)
                earliest = e["time"]
            b_damage += e["bounded"]
            b_hits += 1
            if e["time"] == t and not victimw:
                victimw = e["victimWep"]
        if not anchored:
            continue
        rhits = idx.get((victim, killer), [])
        rd = sum(e["bounded"] for e in rhits if t - contested_ms <= e["time"] <= t)
        rows.append({"killer": killer, "victim": victim, "time": t, "weapon": w,
                     "damage": b_damage, "hits": b_hits,
                     "spanMs": t - earliest, "maxGapMs": maxgap,
                     "victimWep": victimw, "returnDamage": rd})
    rows.sort(key=lambda r: (-r["damage"], r["time"]))
    return rows

if __name__ == "__main__":
    import sys
    d = load(sys.argv[1])
    events = build_recon_events(d)
    rows = assemble_topkills(d, events)
    print(json.dumps({"nevents": len(events), "kills": rows[:5]}, indent=1))
