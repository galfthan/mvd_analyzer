package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
	"github.com/mvd-analyzer/mvd-api/internal/democache"
)

// demoStore is the subset of *democache.Cache the handlers depend on.
// Tests inject a fake.
type demoStore interface {
	GetResult(ctx context.Context, id democache.DemoID) (*result.Result, democache.CacheMeta, error)
	// EnsureLOS returns the Result with the per-player line-of-sight / PVS
	// interval sets materialised (the lazy raycast pass), serialising the
	// compute per demo SHA internally. It persists the result to the tier-3
	// cache so a restart/eviction does not recompute.
	//
	// (There is no EnsureShotStreams: since phase 12 the spatial weapon-fire
	// streams are baked into the always-full GetResult parse, so /shots, /aim
	// and /streams/* read them straight off the base Result.)
	EnsureLOS(ctx context.Context, id democache.DemoID) (*result.Result, democache.CacheMeta, error)

	// PutDemo stores an uploaded demo body under its content SHA and reports
	// the SHA plus whether the demo already existed (POST /v1/demos). The
	// handler then calls GetResult on the returned SHA to parse it.
	PutDemo(ctx context.Context, body []byte) (sha string, existed bool, err error)
	// RemoveDemo best-effort deletes the tier-1 + tier-2 files for a SHA. The
	// upload parse-gate calls it to evict a body that parsed to nothing usable,
	// so the service can't be used as content-addressed storage.
	RemoveDemo(sha string)
}

// httpError carries the wire-format error body.
type httpError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error httpError `json:"error"`
}

// writeError emits the error envelope and the appropriate status. It also
// strips any ETag a success-path header helper may have set before the
// error was decided (setCacheHeaders/setArtifactCacheHeaders run in
// resolveDemo / handleArtifact before the availability check that can 422):
// an ETag on an error body is misleading, and no-store already disables
// caching.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Del("ETag")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: httpError{Code: code, Message: msg}})
}

// genericInternalMsg is the client-facing body for any 5xx: no cache paths
// or upstream URLs, just the request id so an operator can find the real
// error in the server log (F19).
func genericInternalMsg(id string) string {
	if id == "" {
		return "internal server error"
	}
	return "internal server error (request id " + id + ")"
}

// writeInternal logs the real error against the request id and returns the
// generic 5xx body — the single 500 path for the handler layer (F19).
func (s *server) writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	id := requestID(r.Context())
	s.logger.Error("internal error",
		"request_id", id, "method", r.Method, "path", r.URL.Path, "err", err.Error())
	writeError(w, http.StatusInternalServerError, "internal", genericInternalMsg(id))
}

// writeJSON emits a JSON body with the standard cache headers (set by
// the caller via the resp.cacheHeader call before invoking writeJSON).
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// resolveDemo parses the {id} path param, fetches the *Result, and
// sets the cache headers. Returns (r, meta, ok=true) on success; on
// failure, writes the error to w and returns ok=false.
func (s *server) resolveDemo(w http.ResponseWriter, r *http.Request) (*result.Result, democache.CacheMeta, bool) {
	raw := r.PathValue("id")
	id, err := democache.ParseDemoID(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_demo_id", err.Error())
		return nil, democache.CacheMeta{}, false
	}
	res, meta, err := s.store.GetResult(r.Context(), id)
	if err != nil {
		s.mapStoreError(w, r, err)
		return nil, democache.CacheMeta{}, false
	}
	setCacheHeaders(w, meta)
	if revalidated(w, r, meta) {
		return nil, meta, false
	}
	return res, meta, true
}

// revalidated writes a cheap 304 (and reports true) when the request's
// If-None-Match matches meta's ETag. setCacheHeaders must have run first
// so the ETag header is already set. This is the shared conditional-GET
// tail of resolveDemo and the /los handler.
func revalidated(w http.ResponseWriter, r *http.Request, meta democache.CacheMeta) bool {
	etag := fmt.Sprintf(`"%s-v%d"`, meta.SHA256, meta.SchemaVersion)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// mapStoreError maps a democache error to its HTTP status. The 4xx/502
// branches carry the specific (user-actionable, path-free) message; only
// the unclassified default is a 5xx, where the real error goes to the log
// and the client gets the generic request-id body (F19).
func (s *server) mapStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, democache.ErrInvalidDemoID):
		writeError(w, http.StatusBadRequest, "invalid_demo_id", err.Error())
	case errors.Is(err, democache.ErrDemoNotFound):
		writeError(w, http.StatusNotFound, "demo_not_found", err.Error())
	case errors.Is(err, democache.ErrHubUpstream):
		writeError(w, http.StatusBadGateway, "hub_upstream", err.Error())
	default:
		s.writeInternal(w, r, err)
	}
}

// writeUnavailable maps a view.ErrUnavailable (a section the demo lacks
// the enabling signal for) to a 422 with the section-specific code/message,
// and anything else to 500. This is the HTTP face of the R3 rule —
// object-shaped sections that require a capability return 422 when it's
// absent; always-computable / list sections return 200 with an empty body.
func (s *server) writeUnavailable(w http.ResponseWriter, r *http.Request, err error, code, msg string) {
	if errors.Is(err, view.ErrUnavailable) {
		writeError(w, http.StatusUnprocessableEntity, code, msg)
		return
	}
	s.writeInternal(w, r, err)
}

// writeInvalidParam writes the 400 invalid_param envelope for a non-nil
// err — a malformed query param (via qp.Err) or a view-layer rejection of
// an otherwise-parseable value (unknown field code, bad reducer). Reports
// whether it wrote, so callers do `if writeInvalidParam(w, err) { return }`.
func writeInvalidParam(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid_param", err.Error())
	return true
}

// writeUnknownParam writes the 400 unknown_param envelope for a non-nil err
// (an unrecognised query key, from qp.Unknown). Reports whether it wrote, so
// callers do `if writeUnknownParam(w, p.Unknown()) { return }` right after the
// writeInvalidParam(p.Err()) check — the invalid_param → unknown_param →
// missing_param/availability order.
func writeUnknownParam(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	writeError(w, http.StatusBadRequest, "unknown_param", err.Error())
	return true
}

// windowMsZero rejects an explicit windowMs=0 at the HTTP boundary with a 400
// invalid_param (v57 reject-loudly posture, mirroring the games-search limit=0
// rejection): a caller who typed 0 wants zero-width buckets, which is never
// useful, so point them at omitting the param. An OMITTED windowMs keeps the
// endpoint's default (p.Int already resolved it), which is why def is passed
// in — the endpoints do not share one. The view-level <=0 → default coercion
// stays: it is the programmatic-caller default for WASM / qw-analyze, and this
// rejection is only the HTTP surface. Reports whether it wrote, so callers do
// `if windowMsZero(w, p, opts.WindowMs, 50) { return }`.
func windowMsZero(w http.ResponseWriter, p *qp, windowMs, def int) bool {
	if p.Present("windowMs") && windowMs == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("windowMs must be >= 1; omit it for the default %d", def))
		return true
	}
	return false
}

// cacheState is the X-Cache value for the tier that served meta.
func cacheState(meta democache.CacheMeta) string {
	switch {
	case meta.FromCache:
		return "HIT"
	case meta.FromMVDTier:
		return "WARM"
	default:
		return "MISS"
	}
}

func setCacheHeaders(w http.ResponseWriter, meta democache.CacheMeta) {
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("X-Schema-Version", fmt.Sprintf("%d", meta.SchemaVersion))
	w.Header().Set("X-Cache", cacheState(meta))
	w.Header().Set("ETag", fmt.Sprintf(`"%s-v%d"`, meta.SHA256, meta.SchemaVersion))
}

// --- Endpoint handlers ---

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"schemaVersion": result.CurrentSchemaVersion,
	})
}

func (s *server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"hash":      GitHash,
		"tag":       GitTag,
		"buildDate": BuildDate,
	})
}

// handleLoad: POST /v1/demos/{id} — warm the cache for an id and
// return identity metadata. Idempotent.
func (s *server) handleLoad(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	id, err := democache.ParseDemoID(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_demo_id", err.Error())
		return
	}
	_, meta, err := s.store.GetResult(r.Context(), id)
	if err != nil {
		s.mapStoreError(w, r, err)
		return
	}
	// A POST is not a cacheable resource: no Cache-Control/ETag here (they'd
	// be meaningless-to-misleading on the warm-up call). Keep the informational
	// tier + schema headers.
	w.Header().Set("X-Schema-Version", fmt.Sprintf("%d", meta.SchemaVersion))
	w.Header().Set("X-Cache", cacheState(meta))
	writeJSON(w, http.StatusOK, map[string]any{
		"demoId":        "sha:" + meta.SHA256,
		"sha256":        meta.SHA256,
		"fromCache":     meta.FromCache,
		"schemaVersion": meta.SchemaVersion,
	})
}

func (s *server) handleOverview(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	writeJSON(w, http.StatusOK, OverviewEnvelope{TimeUnit: view.UnitMs, Overview: BuildOverview(res)})
}

// handleMetadata: GET /v1/demos/{id}/metadata — full server cvars +
// KTX match settings (timelimit, fraglimit, antilag, midair, spawnmodel,
// instagib, ...). Used by the web's Summary tab.
func (s *server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	md, err := view.Metadata(res)
	if err != nil {
		s.writeUnavailable(w, r, err, "metadata_unavailable",
			"this demo has no metadata (no fullserverinfo / no countdown centerprint)")
		return
	}
	writeJSON(w, http.StatusOK, md)
}

// handleLocGraph: GET /v1/demos/{id}/loc-graph — per-map loc
// adjacency graph (which locs are reachable from which). Used by
// the web's Loc Graph tab.
func (s *server) handleLocGraph(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	lg, err := view.LocGraph(res)
	if err != nil {
		s.writeUnavailable(w, r, err, "locgraph_unavailable",
			"this demo has no loc graph (probably no position track was emitted)")
		return
	}
	writeJSON(w, http.StatusOK, view.LocGraphEnvelope{TimeUnit: view.UnitMs, LocGraphResult: lg})
}

// handleFrags: GET /v1/demos/{id}/frags — top-level frag aggregates +
// the full kill log. Optional filters narrow both views to entries
// involving the named players / weapon. Filtering lives in view.Frags so
// REST, MCP, and WASM share one implementation.
//
// When any scoping filter (players / weapon / from / to) is active, ALL
// aggregates are recomputed from the filtered kill log so they are consistent
// with the entries shown. With no filter the authoritative stored aggregates
// are returned unchanged.
//
// Query params:
//
//	players  csv   — restrict aggregates + the Frags list to entries
//	               where killer or victim is in the set
//	weapons  csv   — restrict aggregates + the Frags list to these weapons
//	               (legacy alias: weapon)
//	from     int — window start, match-relative integer ms (0 = no bound)
//	to       int — window end, match-relative integer ms (0 = no bound)
//	summary  bool  — drop the per-event Frags log; return only aggregates
func (s *server) handleFrags(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.FragOptions{
		Players: p.CSV("players"),
		Weapons: p.CSVAny("weapons", "weapon"),
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
		Summary: p.Bool("summary"),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	out, err := view.Frags(res, opts)
	if err != nil {
		// A bogus weapons token is a 400, not the 422 no-frag-log path.
		if errors.Is(err, view.ErrInvalidFilter) {
			writeInvalidParam(w, err)
			return
		}
		s.writeUnavailable(w, r, err, "frags_unavailable", "this demo has no frag log")
		return
	}
	writeJSON(w, http.StatusOK, view.FragsEnvelope{TimeUnit: view.UnitMs, FragResult: out})
}

// handleDamage: GET /v1/demos/{id}/damage — per-hit damage log +
// aggregates (matrix, per-weapon, given/taken, EWep victim-weapon
// buckets) + the KTX-scoreboard cross-check. Optional filters narrow all
// views to entries involving the named players / weapon.
//
// When any scoping filter (players / weapon / from / to) is active, ALL
// aggregates (totalDamage, byPlayer, byWeapon, matrix) are recomputed from the
// filtered per-hit log so they are consistent with the entries shown. With no
// filter the authoritative stored aggregates are returned unchanged.
//
// The dmg param selects the damage family: raw = the unbound wire
// values (the v53 shape); bounded = KTX-scoreboard semantics materialized into
// the same field names; both = raw fields plus per-player `bounded` nests and a
// per-event `bounded`. Unset resolves here to `bounded` (for both the summary
// and the full-log request). An EXPLICIT dmg=bounded on a demo whose bounded
// reconstruction was skipped (boundedMode skipped:*) is a 422
// bounded_unavailable; a DEFAULTED bounded there falls back to raw instead
// (the raw response's boundedMode explains the absence). A bounded/both SUMMARY
// sources its per-player bounded figures from the exact KTX scoreboard when the
// demo carries demoInfo (boundedSource echoes "ktx" or "reconstructed").
//
// Query params:
//
//	players  csv   — restrict aggregates / Matrix / Events / Scoreboard to
//	               entries where attacker or victim is in the set
//	weapons  csv   — restrict aggregates + Matrix/Events + per-player
//	               ByWeapon to these (attacker) weapons (legacy alias: weapon)
//	from     int — window start, match-relative integer ms (0 = no bound)
//	to       int — window end, match-relative integer ms (0 = no bound)
//	summary  bool  — drop the per-hit Events log; return only aggregates
//	dmg      enum  — raw | bounded | both (default: bounded)
func (s *server) handleDamage(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.DamageOptions{
		Players: p.CSV("players"),
		Weapons: p.CSVAny("weapons", "weapon"),
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
		Summary: p.Bool("summary"),
		Dmg:     p.Dmg(),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// Single resolution point for the damage-family default — the MCP layer
	// inherits it (it proxies the REST call without a dmg param). An unset dmg
	// resolves to "bounded" for BOTH the summary and the full-log request:
	// bounded is the KTX-scoreboard-semantics number a reader almost always
	// wants (per-player given/taken near the scoreboard, no overkill inflation).
	// raw and both stay explicit opt-ins.
	explicitDmg := opts.Dmg != ""
	if opts.Dmg == "" {
		opts.Dmg = "bounded"
	}
	out, err := view.Damage(res, opts)
	// EXCEPTION: a DEFAULTED bounded request on a skipped:* demo (whose bounded
	// family was never reconstructed) falls back to raw instead of 422 — the
	// caller didn't ask for bounded, and the raw response's boundedMode names
	// why it is absent. Only an EXPLICIT dmg=bounded 422s.
	if errors.Is(err, view.ErrBoundedUnavailable) && !explicitDmg {
		opts.Dmg = "raw"
		out, err = view.Damage(res, opts)
	}
	if err != nil {
		// A bogus weapons token is a 400 — it does not wrap ErrUnavailable, so
		// it is checked first (and never triggered the bounded fallback above).
		if errors.Is(err, view.ErrInvalidFilter) {
			writeInvalidParam(w, err)
			return
		}
		// ErrBoundedUnavailable wraps ErrUnavailable, so it must be singled out
		// before the generic writeUnavailable. Only an explicit dmg=bounded
		// reaches here. Name the demo's boundedMode so the caller learns why the
		// bounded family is missing.
		if errors.Is(err, view.ErrBoundedUnavailable) {
			mode := ""
			if res.Damage != nil {
				mode = res.Damage.BoundedMode
			}
			writeError(w, http.StatusUnprocessableEntity, "bounded_unavailable",
				fmt.Sprintf("this demo has no bounded damage family (boundedMode %q); use dmg=raw", mode))
			return
		}
		s.writeUnavailable(w, r, err, "damage_unavailable",
			"this demo has no damage data (no KTX mvdhidden_dmgdone stream)")
		return
	}
	writeJSON(w, http.StatusOK, view.DamageEnvelope{TimeUnit: view.UnitMs, DamageResult: out})
}

// handleShots: GET /v1/demos/{id}/shots — the per-fire weapon stream
// (result.Shots): every detected fire with time/player/weapon/source, hit +
// victims where linkable, per-player match-time aggregates, and the KTX
// reconciliation cross-check. Served from the always-full base parse (like
// /aim), so rl/gl fires carry their projectile-linked hits and ng/sng fires
// their nail-linked ones — the streams are baked into every cached Result
// since phase 12, so this is a plain resolveDemo read (no re-parse, no
// degrade). The former `nails` opt-in param is accepted and ignored: ng/sng
// fires were always in the stream — only their linking was gated.
func (s *server) handleShots(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	// `nails` is the retired opt-in — accepted and ignored (see the doc
	// comment) rather than rejected, so old callers don't 400.
	p := newQP(r.URL.Query())
	p.Accept("nails")
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	sh, err := view.Shots(res)
	if err != nil {
		s.writeUnavailable(w, r, err, "shots_unavailable",
			"this demo has no shot data (no weapon fires decoded)")
		return
	}
	writeJSON(w, http.StatusOK, view.ShotsEnvelope{TimeUnit: view.UnitMs, ShotsResult: sh})
}

// handleAim: GET /v1/demos/{id}/aim — per-player aim analysis (result.Aim):
// per-weapon effectiveness (shots/hits, SG/SSG pellet stats, RL/GL
// direct/splash, the LG near/blocked/out-of-range whiff split), columnar
// crosshair-error samples (hitscan), and the LG ramp series.
//
// Served from the always-full base parse (the projectile/beam/nail streams are
// baked into every cached Result since phase 12), so the stream-derived weapon
// blocks are always present — a plain resolveDemo read.
//
// Optional filters narrow the response. With no time window the stored aim is
// returned (players= selects named shooters' match-wide aim); a from/to window
// recomputes aim over the shots in that window so every figure scopes to it.
// summary drops the big per-fire crosshair + lgRamp blocks — the recommended
// way to avoid overflowing an LLM context.
//
// Query params:
//
//	players  csv   — scope to these shooters
//	from     int — window start, match-relative integer ms (0 = no bound)
//	to       int — window end, match-relative integer ms (0 = no bound)
//	summary  bool  — return only the per-player weapons aggregates
func (s *server) handleAim(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.AimOptions{
		Players: p.CSV("players"),
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
		Summary: p.Bool("summary"),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	am, err := view.Aim(res, opts)
	if err != nil {
		s.writeUnavailable(w, r, err, "aim_unavailable",
			"this demo has no aim data (needs shots + position/view streams)")
		return
	}
	writeJSON(w, http.StatusOK, view.AimEnvelope{TimeUnit: view.UnitMs, AimResult: am})
}

// handleChat: GET /v1/demos/{id}/chat — chat-only slice of
// result.Messages.Events, with optional player / time-window / type
// filters.
//
// Query params:
//
//	from, to   match-relative integer ms, both inclusive
//	players    csv — restrict to these speakers
//	types      csv — defaults to ["chat","teamsay"]; pass a subset to narrow
//
// Returned shape mirrors result.MatchEvent, so callers see Time,
// Type, Player, Team, Message, MessageClean directly (no MCP-event
// envelope, unlike getEvents).
func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.ChatOptions{
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
		Players: p.CSV("players"),
		Types:   p.CSV("types"),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// types is a closed vocabulary {chat, teamsay}; a typo must 400, not
	// silently match nothing. Enum values are case-insensitive (matching
	// every other token filter): lowercase before validating AND before use
	// so Chat()'s case-sensitive ev.Type match (stored lowercase) lines up.
	for i, t := range opts.Types {
		lt := strings.ToLower(t)
		if lt != "chat" && lt != "teamsay" {
			writeError(w, http.StatusBadRequest, "invalid_param",
				fmt.Sprintf("unknown chat type %q; valid: chat, teamsay", t))
			return
		}
		opts.Types[i] = lt
	}
	writeJSON(w, http.StatusOK, view.ChatEnvelope{TimeUnit: view.UnitMs, Messages: view.Chat(res, opts)})
}

// handleDemoInfo: GET /v1/demos/{id}/demoinfo — KTX demoinfo blob
// pass-through. Carries per-player weapon accuracy, kills, deaths,
// damage, sprees, item pickup counts, RL/LG transfers.
func (s *server) handleDemoInfo(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	di, err := view.DemoInfo(res)
	if err != nil {
		s.writeUnavailable(w, r, err, "demoinfo_unavailable",
			"this demo has no KTX demoinfo block (likely non-KTX or pre-match abort)")
		return
	}
	writeJSON(w, http.StatusOK, di)
}

// handlePlayerStats: GET /v1/demos/{id}/player-stats — the canonical
// per-player and per-team statistics row: corrected scoreboard, damage,
// pickup tallies, KTX identity fields, and possession time (time with
// each weapon / armor type / no armor) with explicit denominators.
//
// A missing KTX demoinfo block is never a reason to fail here — the
// section is computed for every demo and each stat family carries `src`
// naming its source (/demoinfo remains the verbatim KTX pass-through to
// diff against). 422 `playerstats_unavailable` means something else: a
// parse degraded enough to produce no player streams at all.
//
// Query params:
//
//	players  csv — restrict to these players (drops the team rows, which
//	             are whole-team sums and would misread as the subset's)
//	teams    csv — restrict to these teams
func (s *server) handlePlayerStats(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.PlayerStatsOptions{
		Players: p.CSV("players"),
		Teams:   p.CSV("teams"),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	ps, err := view.PlayerStats(res, opts)
	if err != nil {
		s.writeUnavailable(w, r, err, "playerstats_unavailable",
			"this demo produced no player streams (degraded parse) — note a missing KTX demoinfo block is NOT a reason for this")
		return
	}
	writeJSON(w, http.StatusOK, view.PlayerStatsEnvelope{TimeUnit: view.UnitMs, PlayerStatsResult: ps})
}

// handleBackpacks: GET /v1/demos/{id}/backpacks — RL/LG drops with
// optional player/weapon filters.
//
// Query params:
//
//	players  csv — restrict to drops by these dropper names
//	weapons  csv — restrict to these weapons ("rl"/"lg"; case-insensitive;
//	             legacy alias: weapon)
//	from/to  match-relative integer ms — window the drop time
func (s *server) handleBackpacks(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.BackpackOptions{
		Players: p.CSV("players"),
		Weapons: p.CSVAny("weapons", "weapon"),
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	bp, err := view.Backpacks(res, opts)
	if writeInvalidParam(w, err) { // only ErrInvalidFilter (bogus weapons token) → 400
		return
	}
	writeJSON(w, http.StatusOK, view.BackpacksEnvelope{TimeUnit: view.UnitMs, Backpacks: bp})
}

// handleItems: GET /v1/demos/{id}/items — per-item pickup/respawn
// timeline with optional filters.
//
// Query params (all case-insensitive):
//
//	items    csv — restrict to items whose Name or Kind matches. Accepts
//	             a kind token to match every instance of a type ("ya" →
//	             ya_1, ya_2; "ra"; "mh") or a specific instance Name
//	             ("ya_1"). RA/YA/GA/MH/Quad/Pent/Ring/RL/LG all work.
//	players  csv — restrict to phases where TakenBy is one of these names
//	kinds    csv — restrict to item categories: armor, mega, health,
//	             powerup, weapon, ammo (see ItemTimeline.Category). A raw
//	             kind token ("ra", "quad") is also accepted.
//
// items/kinds match the canonical lowercase tokens regardless of input
// case; players is matched against the exact display name (case-
// sensitive — QW names are case-significant).
//
// Phases with no TakenBy survive any players= filter (they represent
// the item's availability state at match end / dropped runs).
//
//	from/to  match-relative integer ms — keep phases OVERLAPPING the window
//	         (a phase covers [availableFrom, respawnAt), open-ended when
//	         respawnAt is 0)
//	summary  bool — per-item take aggregates (takenCount, byPlayer,
//	         firstTake) instead of the phase timeline; takes are counted
//	         INSIDE the window
func (s *server) handleItems(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.ItemOptions{
		Items:   p.CSV("items"),
		Players: p.CSV("players"),
		Kinds:   p.CSV("kinds"),
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
	}
	summary := p.Bool("summary")
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// Both shapes are int32 ms (v57 pure-ms model): the full phase timeline
	// (availableFrom/takenAt/respawnAt) and the summary firstTake.t alike.
	// timeUnit echoes the constant "ms".
	if summary {
		sv := view.ItemsSummary(res, opts)
		sv.TimeUnit = view.UnitMs
		writeJSON(w, http.StatusOK, sv)
		return
	}
	writeJSON(w, http.StatusOK, view.ItemsEnvelope{TimeUnit: view.UnitMs, ItemsResult: view.Items(res, opts)})
}

// handleWeaponPickups: GET /v1/demos/{id}/weapon-pickups — slot-weapon
// acquisitions with effectiveness (kills-before-next-death). Optional
// filters by player / weapon / source.
//
// Query params:
//
//	players  csv — restrict to picks by these names
//	weapons  csv — "rl","lg","gl","ssg","sng","ng" (case-insensitive;
//	             legacy alias: weapon)
//	source   "world" | "backpack" | "unknown"
//	from/to  match-relative integer ms — window the pickup time
func (s *server) handleWeaponPickups(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.WeaponPickupOptions{
		Players: p.CSV("players"),
		Weapons: p.CSVAny("weapons", "weapon"),
		Source:  p.Str("source"),
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// source is an enum like loc/layout — a typo must 400, not silently
	// match nothing (the other enum params already reject unknowns).
	switch strings.ToLower(opts.Source) {
	case "", "world", "backpack", "unknown":
	default:
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("unknown source %q; valid: world, backpack, unknown", opts.Source))
		return
	}
	wp, err := view.WeaponPickups(res, opts)
	if writeInvalidParam(w, err) { // only ErrInvalidFilter (bogus weapons token) → 400
		return
	}
	writeJSON(w, http.StatusOK, view.WeaponPickupsEnvelope{TimeUnit: view.UnitMs, Pickups: wp})
}

func (s *server) handleBuckets(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.BucketsOptions{
		WindowMs:    p.Int("windowMs", 50),
		StartTime:   p.Ms("from", 0),
		EndTime:     p.Ms("to", 0),
		Players:     p.CSV("players"),
		Fields:      p.CSV("fields"),
		Reducers:    p.Reducers("reducers"),
		IncludeTeam: p.Bool("includeTeam"),
		LocIndex:    p.LocIndex(),
		Layout:      p.Layout(),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	if windowMsZero(w, p, opts.WindowMs, 50) {
		return
	}
	if opts.Layout == "column" {
		cb, err := view.BucketsColumnar(res, opts)
		if writeInvalidParam(w, err) {
			return
		}
		cb.TimeUnit = view.UnitMs
		writeJSON(w, http.StatusOK, cb)
		return
	}
	bv, err := view.Buckets(res, opts)
	if writeInvalidParam(w, err) {
		return
	}
	bv.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, bv)
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	filter := view.EventsFilter{
		StartTime: p.Ms("from", 0),
		EndTime:   p.Ms("to", 0),
		Players:   p.CSV("players"),
		Types:     p.CSV("types"),
		LocIndex:  p.LocIndex(),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	ev, err := view.Events(res, filter)
	if writeInvalidParam(w, err) {
		return
	}
	ev.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, ev)
}

func (s *server) handleStreamSlice(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.StreamSliceOptions{
		Start:    p.Ms("from", 0),
		End:      p.Ms("to", 0),
		Players:  p.CSV("players"),
		Fields:   p.CSV("fields"),
		LocIndex: p.LocIndex(),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	sl, err := view.StreamSlice(res, opts)
	if writeInvalidParam(w, err) {
		return
	}
	sl.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, sl)
}

func (s *server) handleStateAt(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	p := newQP(q)
	opts := view.StateAtOptions{
		Time:     p.Ms("time", 0),
		Players:  p.CSV("players"),
		Fields:   p.CSV("fields"),
		LocIndex: p.LocIndex(),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// missing_param comes after the param-hygiene checks (invalid → unknown →
	// missing): a bad or unknown param wins over the absent-time report.
	if ciGet(q, "time") == "" {
		writeError(w, http.StatusBadRequest, "missing_param", "time is required")
		return
	}
	sa, err := view.StateAt(res, opts)
	if writeInvalidParam(w, err) {
		return
	}
	sa.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, sa)
}

// handleLOS: GET /v1/demos/{id}/los — per-player line-of-sight intervals.
//
// Line of sight is the heaviest position-derived pass and has no other
// consumer, so it is computed lazily via EnsureLOS: the first request for a
// demo triggers the raycast pass (serialised per SHA in the cache) and writes
// the result to the tier-3 artifact cache, so later requests — and later
// processes, after a restart or an LRU eviction — splice it from disk instead
// of recomputing. The tier-2 gob stays lean — LOS is never baked into it.
// Returns 200 with a players array (los omitted for a player with no
// sightlines); 422 los_unavailable when the map has no usable visibility BSP
// (no map name, BSP not provisioned, or a provisioned BSP that won't parse) —
// that outcome is never latched or cached, so provisioning the BSP heals it.
func (s *server) handleLOS(w http.ResponseWriter, r *http.Request) {
	id, err := democache.ParseDemoID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_demo_id", err.Error())
		return
	}
	// Reject unknown params BEFORE EnsureLOS: LOS is the heaviest lazy pass,
	// and a typo'd param must not trigger the raycast compute.
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	res, meta, err := s.store.EnsureLOS(r.Context(), id)
	if err != nil {
		if errors.Is(err, analyzer.ErrNoBSP) {
			writeError(w, http.StatusUnprocessableEntity, "los_unavailable",
				"line of sight needs the map's visibility BSP, which is not available for this demo")
			return
		}
		s.mapStoreError(w, r, err)
		return
	}
	setCacheHeaders(w, meta)
	if revalidated(w, r, meta) {
		return
	}
	writeJSON(w, http.StatusOK, losBody(res))
}

// losBody is the /los (and `los` artifact) response body: per-player LOS/PVS
// interval sets. Shared so the curated endpoint and the generic artifact
// endpoint never fork the shape. Only reached on a successful (200) compute —
// a map with no usable BSP is a 422 los_unavailable before this runs, so this
// never renders an all-empty body for a BSP-less map.
func losBody(res *result.Result) any {
	type losPlayer struct {
		Name string            `json:"name"`
		LOS  []result.LosTrack `json:"los,omitempty"`
		PVS  []result.LosTrack `json:"pvs,omitempty"`
	}
	out := struct {
		TimeUnit view.TimeUnit `json:"timeUnit"`
		Players  []losPlayer   `json:"players"`
	}{TimeUnit: view.UnitMs, Players: []losPlayer{}} // never null: the doc + API.md promise a players array. Intervals are int32-ms → ms.
	if res.Streams != nil {
		out.Players = make([]losPlayer, len(res.Streams.Players))
		for i := range res.Streams.Players {
			out.Players[i].Name = res.Streams.Players[i].Name
			out.Players[i].LOS = res.Streams.Players[i].LOS
			out.Players[i].PVS = res.Streams.Players[i].PVS
		}
	}
	return out
}

// handleProjectiles serves the rocket/grenade flight stream. Body is
// {"projectiles": ...}, null when the demo has none. The streams are baked
// into the always-full base parse (phase 12), so this is a plain resolveDemo
// read.
func (s *server) handleProjectiles(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	var pr *result.ProjectileStreams
	if res.Streams != nil {
		pr = res.Streams.Projectiles
	}
	writeJSON(w, http.StatusOK, struct {
		TimeUnit    view.TimeUnit             `json:"timeUnit"`
		Projectiles *result.ProjectileStreams `json:"projectiles"`
	}{view.UnitMs, pr})
}

// handleBeams serves the LG bolt stream (from the always-full base parse).
func (s *server) handleBeams(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	var bm *result.BeamStreams
	if res.Streams != nil {
		bm = res.Streams.Beams
	}
	writeJSON(w, http.StatusOK, struct {
		TimeUnit view.TimeUnit       `json:"timeUnit"`
		Beams    *result.BeamStreams `json:"beams"`
	}{view.UnitMs, bm})
}

// handleNails serves the ng/sng nail-flight stream (highest volume; from the
// always-full base parse).
func (s *server) handleNails(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	var nl *result.ProjectileStreams
	if res.Streams != nil {
		nl = res.Streams.Nails
	}
	writeJSON(w, http.StatusOK, struct {
		TimeUnit view.TimeUnit             `json:"timeUnit"`
		Nails    *result.ProjectileStreams `json:"nails"`
	}{view.UnitMs, nl})
}

func (s *server) handleLocTrails(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	// Field order here fixes which of several malformed params is reported:
	// keep from, to, minDwellMs, loc — the historical read order.
	opts := view.LocTrailsOptions{
		StartTime:  p.Ms("from", 0),
		EndTime:    p.Ms("to", 0),
		MinDwellMs: p.Int("minDwellMs", 0),
		Players:    p.CSV("players"),
		LocIndex:   p.LocIndex(),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	tr, err := view.LocTrails(res, opts)
	if writeInvalidParam(w, err) {
		return
	}
	tr.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, tr)
}

// handleLocTable: GET /v1/demos/{id}/loc-table — the interned loc-name
// table, the decoder for the `li` indices returned by the loc-bearing
// views in index mode (?loc=index). Index 0 is the "" no-loc sentinel.
// Empty array when the demo carried no loc data.
func (s *server) handleLocTable(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	table := []string{}
	if res.TimelineAnalysis != nil && res.TimelineAnalysis.LocTable != nil {
		table = res.TimelineAnalysis.LocTable
	}
	writeJSON(w, http.StatusOK, map[string]any{"locTable": table})
}

func (s *server) handleRegionControl(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	// Param hygiene (invalid → unknown) runs BEFORE the availability 422: a
	// typo'd param on a demo that also lacks region control must 400, not 422.
	p := newQP(r.URL.Query())
	opts := view.RegionControlOptions{
		WindowMs:  p.Int("windowMs", 50),
		StartTime: p.Ms("from", 0),
		EndTime:   p.Ms("to", 0),
	}
	regionsMode := p.Regions()
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	if windowMsZero(w, p, opts.WindowMs, 50) {
		return
	}
	if err := view.RegionControlAvailable(res); err != nil {
		s.writeUnavailable(w, r, err, "region_control_unavailable", "this demo has no region-control layout")
		return
	}
	rcv, err := view.RegionControl(res, opts)
	if writeInvalidParam(w, err) {
		return
	}
	// Shallow value copy of the view result so we can vary Regions per mode
	// without mutating the stored Result — embedding the copy keeps the body
	// in lock-step with the RegionControlResult shape (no shadow envelope).
	body := *rcv
	switch regionsMode {
	case "summary":
		// Copy each region and drop its Points polygon (~6KB total) — never
		// mutate the stored Result's slice. name/locs/centroids are kept.
		slim := make([]result.ControlRegion, len(rcv.Regions))
		for i, rg := range rcv.Regions {
			rg.Points = nil
			slim[i] = rg
		}
		body.Regions = slim
	case "none":
		// Regions omitted from the response entirely (regions,omitempty).
		body.Regions = nil
	default: // "full"
		// body.Regions is already rcv.Regions.
	}
	writeJSON(w, http.StatusOK, view.RegionControlEnvelope{TimeUnit: view.UnitMs, RegionControlResult: &body})
}

// handleAirgibs: GET /v1/demos/{id}/airgibs — the Key Moments airgib list
// (timelineAnalysis.airgibs): every DIRECT enemy rocket hit on an airborne
// victim above the height threshold, sorted by height descending. Height
// needs the map's clip hull, so the list is empty (not an error) when the
// map's BSP was not provisioned at parse time.
func (s *server) handleAirgibs(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	airgibs, err := view.Airgibs(res)
	if err != nil {
		s.writeUnavailable(w, r, err, "airgibs_unavailable",
			"this demo has no timeline analysis")
		return
	}
	writeJSON(w, http.StatusOK, view.AirgibsEnvelope{TimeUnit: view.UnitMs, Airgibs: airgibs})
}

// handleTopWindows: GET /v1/demos/{id}/top-windows — each player's best
// stretches of the match, computed on demand from the stored event logs.
//
// Two segmentations, one sentence each. Fixed (the default): "in these
// windowMs milliseconds this player scored higher on metric than in any other
// stretch of the same length." Gap: "a window is a maximal run of scoring
// events in which consecutive events are no more than gapMs apart; its score
// is their sum." Windows are anchored at real event times (not a grid),
// non-overlapping per player, and returned as one flat list under two
// independent caps — perPlayer, then limit.
//
// Query params:
//
//	metric     enum — frags (default) | deaths | netFrags | damageGiven |
//	                  damageTaken | netDamage | shots | hits
//	mode       enum — fixed (default) | gap — the segmentation. Each mode
//	                  REJECTS the other's knob with a 400 rather than
//	                  ignoring it, and the resolved value is echoed on every
//	                  response, fixed ones included.
//	windowMs   int  — fixed mode's window length in ms (default 30000; an
//	                  explicit value below 1 or above the match duration is a
//	                  400). Rejected under mode=gap.
//	gapMs      int  — gap mode's inter-event gap in ms, REQUIRED there: it has
//	                  no default (the metrics' cadences are too far apart for
//	                  one value — the 400 names the measured starting points).
//	                  Below 1 or above the match duration is a 400. Rejected
//	                  under mode=fixed.
//	limit      int  — total windows (default 10; negative means uncapped; an
//	                  explicit 0 and anything above 200 are both a 400, never
//	                  a silent clamp)
//	perPlayer  int  — max windows from any one player (default uncapped;
//	                  negative means uncapped, explicit 0 is a 400 — limit's
//	                  rule exactly)
//	players    csv  — restrict to these SUBJECT players
//	weapons    csv  — restrict the SCORING events (legacy alias: weapon)
//	from / to  int  — match-relative bounds, integer ms (0 = no bound). NOTE
//	                  they bound where a window may START, not what it covers:
//	                  a window anchored at `to` still runs windowMs past it.
//	dmg        enum — raw | bounded (default bounded); both is rejected
//	minScore   int  — drop windows scoring below this (default 1)
func (s *server) handleTopWindows(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	// windowMs is read as a plain int, not through Ms: Ms rejects a negative
	// with "must be >= 0" while the endpoint's real floor is 1, so a caller
	// told to retry with 0 failed again. One check below covers both.
	windowMs := p.IntHint("windowMs", 0, "integer milliseconds")
	// gapMs reads exactly the same way, for the same reason.
	gapMs := p.IntHint("gapMs", 0, "integer milliseconds")
	opts := view.TopWindowsOptions{
		Metric: p.Metric(),
		// mode is forwarded UNVALIDATED. view.TopWindows owns the vocabulary
		// and all three cross-knob rules (an unknown mode, either knob under
		// the wrong mode, a missing gapMs under gap), and its ErrInvalidFilter
		// lands on the 400 invalid_param path at the tail of this handler.
		// Re-stating those messages here would give one request two wordings
		// depending on which layer happened to see it first.
		Mode:      p.Str("mode"),
		Limit:     p.Int("limit", 0),
		PerPlayer: p.Int("perPlayer", 0),
		Players:   p.CSV("players"),
		Weapons:   p.CSVAny("weapons", "weapon"),
		From:      p.Ms("from", 0),
		To:        p.Ms("to", 0),
		Dmg:       p.DmgFamily(),
		Min:       p.IntPtr("minScore", "integer score, may be negative"),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// Explicit sentinels are REJECTED, not silently reinterpreted — the v59
	// ruling that an out-of-range limit "is rejected, no longer silently
	// clamped". An omitted MCP int arrives as 0, which is why 0 cannot mean
	// "uncapped" for either cap: it would make a forgotten argument look like
	// a deliberate one.
	if p.Present("windowMs") && windowMs < 1 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("windowMs must be >= 1; omit it for the default %d", topWindowsDefaultMs))
		return
	}
	// An UNBOUNDED windowMs makes the documented `end = start + windowMs`
	// unsatisfiable: the view has to clamp end to MaxInt32, and windowMs=
	// 2147483647 then reported a 24.8-day "window" carrying the whole match's
	// stats, on which every derived rate is nonsense. The match duration is
	// the natural cap — a window at least that long is the whole match, and
	// asking for more describes time that does not exist. The view's clamp
	// stays as defence in depth for its in-process callers; this makes it
	// unreachable over HTTP.
	maxSpan := matchDurationMs(res)
	if windowMs > maxSpan {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("windowMs must be <= %d (the match duration), got %d; a window longer than the match is the whole match", maxSpan, windowMs))
		return
	}
	opts.WindowMs = int32(windowMs)
	// gapMs is bounded at both ends exactly as windowMs is, on the same
	// match duration — with one deliberate difference in wording. The floor
	// message must NOT say "omit it for the default": gap mode has none, and
	// omitting it under mode=gap is itself a 400 (the view's message, which
	// names the measured starting points). Which mode the request asked for is
	// not consulted here; a gapMs that is out of range is out of range whether
	// or not it also belongs to the other mode, and the conflict 400 the view
	// raises for an in-range one says the same thing more precisely.
	if p.Present("gapMs") && gapMs < 1 {
		writeError(w, http.StatusBadRequest, "invalid_param", "gapMs must be >= 1")
		return
	}
	if gapMs > maxSpan {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("gapMs must be <= %d (the match duration), got %d; a gap that long joins every one of a player's events into a single window", maxSpan, gapMs))
		return
	}
	opts.GapMs = int32(gapMs)
	if p.Present("limit") && opts.Limit == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			"limit must be >= 1, or negative for uncapped; omit it for the default 10")
		return
	}
	if opts.Limit > topWindowsMaxLimit {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("limit must be <= %d, got %d", topWindowsMaxLimit, opts.Limit))
		return
	}
	// perPlayer follows limit's rule exactly rather than inventing a second
	// one: an explicit 0 is rejected (the omitted-MCP-integer reason applies
	// verbatim), a negative means uncapped, and omitting it is the uncapped
	// default. The old shape rejected -1 and accepted 0, i.e. the opposite of
	// limit on the same endpoint, so an agent that learned one guessed the
	// other wrong in both directions.
	if p.Present("perPlayer") && opts.PerPlayer == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			"perPlayer must be >= 1, or negative for uncapped; omit it for the default (uncapped)")
		return
	}
	if err := view.TopWindowsAvailable(res, opts.Metric); err != nil {
		s.writeUnavailable(w, r, err, "top_windows_unavailable",
			topWindowsUnavailableMsg(opts.Metric))
		return
	}

	// Single resolution point for the damage-family default, matching
	// handleDamage: an unset dmg is bounded, and a DEFAULTED bounded on a
	// skipped:* demo falls back to raw rather than 422-ing a caller who never
	// asked for the bounded family.
	explicitDmg := opts.Dmg != ""
	if !explicitDmg {
		opts.Dmg = "bounded"
	}
	out, err := view.TopWindows(res, opts)
	if errors.Is(err, view.ErrBoundedUnavailable) && !explicitDmg {
		opts.Dmg = "raw"
		out, err = view.TopWindows(res, opts)
	}
	if err != nil {
		// A bogus weapons token is a 400 — it does not wrap ErrUnavailable, so
		// it is checked first. The gate is on ErrInvalidFilter and NOT on
		// "err != nil": ErrBoundedUnavailable wraps ErrUnavailable, so an
		// unconditional writeInvalidParam here swallowed every 422 below and
		// answered 400 invalid_param instead. Same order as handleDamage.
		if errors.Is(err, view.ErrInvalidFilter) {
			writeInvalidParam(w, err)
			return
		}
		if errors.Is(err, view.ErrBoundedUnavailable) {
			mode := ""
			if res.Damage != nil {
				mode = res.Damage.BoundedMode
			}
			writeError(w, http.StatusUnprocessableEntity, "bounded_unavailable",
				fmt.Sprintf("this demo has no bounded damage family (boundedMode %q); use dmg=raw", mode))
			return
		}
		s.writeUnavailable(w, r, err, "top_windows_unavailable",
			topWindowsUnavailableMsg(opts.Metric))
		return
	}
	out.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, out)
}

// topWindowsMaxLimit mirrors view's own cap. Duplicated deliberately: the
// handler REJECTS an over-limit request rather than letting the view clamp it,
// so the number has to be known here to say what the bound is. (The view still
// clamps behind us, as defence in depth for its in-process callers.)
//
// topWindowsDefaultMs is likewise view's default window, needed here only so
// the windowMs=0 rejection can name the value the caller gets by omitting it.
const (
	topWindowsMaxLimit  = 200
	topWindowsDefaultMs = 30000
)

// topWindowsUnavailableMsg names the stream the chosen metric needs, so a 422
// says which capability the demo lacks rather than just "unavailable".
func topWindowsUnavailableMsg(metric string) string {
	switch metric {
	case view.MetricShots, view.MetricHits:
		return "this demo has no weapon-fire stream"
	case view.MetricDamageGiven, view.MetricDamageTaken, view.MetricNetDamage:
		return "this demo has no damage data (no KTX mvdhidden_dmgdone stream)"
	default:
		return "this demo has no frag log"
	}
}

// topKillsUnavailableMsg names all THREE sources view.TopKillsAvailable
// needs. The third is the one a caller would never guess: the burst walk is
// clipped by the victim's current life start, so a demo whose liveness was
// never measurable cannot be served without letting bursts absorb the victim's
// previous life — and because the list is ranked BY damage, those are exactly
// the rows that reach the top.
const topKillsUnavailableMsg = "this demo has no frag log, no damage data (no KTX mvdhidden_dmgdone stream), or no measurable liveness to clip the bursts with"

// topKills* mirror view's own defaults and caps. Duplicated for the same
// reason the top-windows constants are: the handler REJECTS an out-of-range
// request instead of letting the view clamp it, so it has to know the numbers
// to name them in the 400. (The view still clamps behind us, as defence in
// depth for its in-process callers.)
const (
	topKillsDefaultGapMs       = 3000
	topKillsMaxGapMs           = 5000
	topKillsDefaultContestedMs = 4000
	topKillsMaxContestedMs     = 30000
	topKillsDefaultLimit       = 20
	topKillsMaxLimit           = 200
)

// handleTopKills: GET /v1/demos/{id}/top-kills — the match's hardest kill
// BURSTS, ranked by burst damage.
//
// For each enemy kill the burst is the contiguous run of KILLING-WEAPON hits
// the killer landed on that victim leading up to it. That is the endpoint's
// meaning, not an approximation of "what the kill took": on ~8% of measured
// kills a mixed-weapon kill (rocket softens, shotgun finishes) reports only
// the finishing weapon's share, and /damage stays the place to ask the other
// question.
//
// Query params:
//
//	gapMs       int  — CAPTURE gap of the backward walk (default 3000; an
//	                   explicit value outside [1, 5000] is a 400). Generous on
//	                   purpose: truncation is unrecoverable downstream while
//	                   over-merge is filterable — keep the rows with
//	                   maxGapMs <= g to reproduce the gap-g walk exactly.
//	contestedMs int  — window returnDamage sums the victim's damage back over
//	                   (default 4000; outside [1, 30000] is a 400)
//	limit       int  — rows returned (default 20; negative means uncapped; an
//	                   explicit 0 and anything above 200 are both a 400, never
//	                   a silent clamp — /top-windows' rule verbatim)
//	players     csv  — restrict to these KILLERS
//	weapons     csv  — restrict the KILLING weapon (legacy alias: weapon)
//	minDamage   int  — drop bursts below this many points (default 0)
//	from / to   int  — bound the KILL instant, integer ms (0 = no bound).
//	                   Unlike /top-windows' window anchors these bound the row
//	                   itself; the burst behind it may reach back before `from`.
//	dmg         enum — raw | bounded (default bounded); both is rejected
func (s *server) handleTopKills(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	// gapMs / contestedMs are read as plain ints rather than through Ms: Ms
	// rejects a negative with "must be >= 0" while both floors are 1, so a
	// caller told to retry with 0 would fail again. The range checks below say
	// it once, with both bounds.
	gapMs := p.IntHint("gapMs", 0, "integer milliseconds")
	contestedMs := p.IntHint("contestedMs", 0, "integer milliseconds")
	opts := view.TopKillsOptions{
		Limit:     p.Int("limit", 0),
		Players:   p.CSV("players"),
		Weapons:   p.CSVAny("weapons", "weapon"),
		MinDamage: p.IntHint("minDamage", 0, "integer damage, >= 0"),
		From:      p.Ms("from", 0),
		To:        p.Ms("to", 0),
		Dmg:       p.DmgFamily(),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// Out-of-range durations are REJECTED, not clamped (the v59 ruling). Both
	// are echoed on the response, so a silent clamp would answer a question
	// the caller did not ask and only say so in a field they may not read.
	if p.Present("gapMs") && (gapMs < 1 || gapMs > topKillsMaxGapMs) {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("gapMs must be between 1 and %d, got %d; omit it for the default %d",
				topKillsMaxGapMs, gapMs, topKillsDefaultGapMs))
		return
	}
	if p.Present("contestedMs") && (contestedMs < 1 || contestedMs > topKillsMaxContestedMs) {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("contestedMs must be between 1 and %d, got %d; omit it for the default %d",
				topKillsMaxContestedMs, contestedMs, topKillsDefaultContestedMs))
		return
	}
	opts.GapMs = int32(gapMs)
	opts.ContestedMs = int32(contestedMs)
	// limit follows /top-windows' shipped semantics exactly — an omitted MCP
	// integer arrives as 0, so 0 cannot mean "uncapped" without making a
	// forgotten argument look deliberate.
	if p.Present("limit") && opts.Limit == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("limit must be >= 1, or negative for uncapped; omit it for the default %d", topKillsDefaultLimit))
		return
	}
	if opts.Limit > topKillsMaxLimit {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("limit must be <= %d, got %d", topKillsMaxLimit, opts.Limit))
		return
	}
	// A negative minDamage is not a second spelling of "no filter" — 0 already
	// is one, since a burst's damage is non-negative.
	if opts.MinDamage < 0 {
		writeError(w, http.StatusBadRequest, "invalid_param", "minDamage must be >= 0")
		return
	}
	if err := view.TopKillsAvailable(res); err != nil {
		s.writeUnavailable(w, r, err, "top_kills_unavailable", topKillsUnavailableMsg)
		return
	}

	// Same damage-family default resolution as handleDamage / handleTopWindows
	// / handleLives: an unset dmg is bounded, and a DEFAULTED bounded on a
	// skipped:* demo falls back to raw rather than 422-ing a caller who never
	// asked for the bounded family.
	explicitDmg := opts.Dmg != ""
	if !explicitDmg {
		opts.Dmg = "bounded"
	}
	out, err := view.TopKills(res, opts)
	if errors.Is(err, view.ErrBoundedUnavailable) && !explicitDmg {
		opts.Dmg = "raw"
		out, err = view.TopKills(res, opts)
	}
	if err != nil {
		// ErrInvalidFilter first, then the bounded 422, then the generic one —
		// the order handleDamage documents: ErrBoundedUnavailable WRAPS
		// ErrUnavailable, and writeInvalidParam 400s on any non-nil error, so
		// an unconditional writeInvalidParam here would make both 422s below
		// unreachable.
		if errors.Is(err, view.ErrInvalidFilter) {
			writeInvalidParam(w, err)
			return
		}
		if errors.Is(err, view.ErrBoundedUnavailable) {
			mode := ""
			if res.Damage != nil {
				mode = res.Damage.BoundedMode
			}
			writeError(w, http.StatusUnprocessableEntity, "bounded_unavailable",
				fmt.Sprintf("this demo has no bounded damage family (boundedMode %q); use dmg=raw", mode))
			return
		}
		s.writeUnavailable(w, r, err, "top_kills_unavailable", topKillsUnavailableMsg)
		return
	}
	out.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, out)
}

// livesUnavailableMsg names BOTH halves of view.LivesAvailable's gate. The
// second half is not a footnote: a demo can carry per-player streams and still
// have no measurable liveness (PlayerStream.Alive nil on every one of them),
// and a message naming only the streams sent that caller looking for a stream
// they have.
const livesUnavailableMsg = "this demo has no per-player streams to segment into lives, or none on which liveness was measurable"

// handleLives: GET /v1/demos/{id}/lives — one row per spawn-to-death run, with
// the same per-interval stats block top windows uses.
//
// Query params:
//
//	players   csv  — restrict to these players
//	from / to int  — keep lives OVERLAPPING this window, integer ms
//	minMs     int  — drop lives shorter than this
//	dmg       enum — raw | bounded (default bounded)
//	summary   bool — keep every row and every scalar, drop the per-row
//	                 breakdown collections (see livesSummary)
func (s *server) handleLives(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.resolveDemo(w, r)
	if !ok {
		return
	}
	p := newQP(r.URL.Query())
	opts := view.LivesOptions{
		Players: p.CSV("players"),
		From:    p.Ms("from", 0),
		To:      p.Ms("to", 0),
		MinMs:   p.Ms("minMs", 0),
		Dmg:     p.DmgFamily(),
	}
	summary := p.Bool("summary")
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	if opts.MinMs < 0 {
		writeError(w, http.StatusBadRequest, "invalid_param", "minMs must be >= 0")
		return
	}
	if err := view.LivesAvailable(res); err != nil {
		s.writeUnavailable(w, r, err, "lives_unavailable",
			livesUnavailableMsg)
		return
	}

	explicitDmg := opts.Dmg != ""
	if !explicitDmg {
		opts.Dmg = "bounded"
	}
	out, err := view.Lives(res, opts)
	if errors.Is(err, view.ErrBoundedUnavailable) && !explicitDmg {
		opts.Dmg = "raw"
		out, err = view.Lives(res, opts)
	}
	if err != nil {
		// ErrInvalidFilter first, for the same reason handleDamage does it:
		// ErrBoundedUnavailable wraps ErrUnavailable, so gating on a bare
		// non-nil err turned the 422 below into dead code.
		if errors.Is(err, view.ErrInvalidFilter) {
			writeInvalidParam(w, err)
			return
		}
		if errors.Is(err, view.ErrBoundedUnavailable) {
			mode := ""
			if res.Damage != nil {
				mode = res.Damage.BoundedMode
			}
			writeError(w, http.StatusUnprocessableEntity, "bounded_unavailable",
				fmt.Sprintf("this demo has no bounded damage family (boundedMode %q); use dmg=raw", mode))
			return
		}
		s.writeUnavailable(w, r, err, "lives_unavailable",
			livesUnavailableMsg)
		return
	}
	if summary {
		livesSummary(out)
	}
	out.TimeUnit = view.UnitMs
	writeJSON(w, http.StatusOK, out)
}

// livesSummary is /lives' size control, the sibling of /damage's and /items'
// summary=true.
//
// Lives is the one heavy endpoint whose from/to only SELECT rows: each kept
// row still carries its whole attribution window, so a narrowed window does
// not narrow the row. Measured on gameId:212260 (387 lives, 241 KB) the weight
// is in the per-row breakdown collections — itemsTaken 45 KB, locs 29 KB,
// eventLocs 7 KB, victims/byWeapon/damageByWeapon 11 KB — not in the scalars.
//
// So the rule is: keep every ROW and every SCALAR (the counts, the durations,
// endReason, the locs a life began and ended in, killedBy/deathWeapon,
// mainWeapon, weaponsHeld) and drop the collections. A caller who needs one
// life's detail re-asks with players= and from/to.
//
// itemsTaken is set to null rather than []: [] would claim this life took
// nothing. In a summary response null means "not requested" and carries NO
// information about the demo — the envelope's measured.items is still the
// authority on whether an item timeline exists, which is where the schema
// already points readers. Documented on the parameter.
//
// This trims mvd-api's own response, not the view: view.Lives has no Summary
// option, and adding one there is mvd-analytics' call.
func livesSummary(out *view.LivesView) {
	for i := range out.Lives {
		l := &out.Lives[i]
		l.ItemsTaken = nil
		l.Locs = nil
		l.EventLocs = nil
		l.Victims = nil
		l.ByWeapon = nil
		l.DamageByWeapon = nil
	}
}

// matchDurationMs is how long the match ran, in ms — the bound top windows
// caps windowMs against. Streams.Global.MatchEnd is the authority (MatchStart
// is always 0, the match-relative origin); Match.Duration is the fallback for
// a Result assembled without streams. Both absent leaves a half-int32 ceiling,
// which keeps `start + windowMs` inside int32 without pretending to know a
// duration the Result never carried.
func matchDurationMs(r *result.Result) int {
	var d int32
	if r.Streams != nil && r.Streams.Global.MatchEnd > 0 {
		d = r.Streams.Global.MatchEnd - r.Streams.Global.MatchStart
	}
	if d <= 0 && r.Match != nil {
		d = r.Match.Duration
	}
	if d <= 0 {
		return math.MaxInt32 / 2
	}
	return int(d)
}
