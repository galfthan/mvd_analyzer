# PLAN-hosting — public API + MCP serving, Discord-keyed portal

> Written 2026-07-07 on `review` (post phase-12). This plan pulls the
> hosting-prep cluster forward — hosting is now the next deliverable
> (decision 2026-07-07, reversing the 2026-07-06 "not imminent" call) —
> and adds the new serving surface on top: API-key auth, a minimal
> Discord-authenticated key portal, and MCP over streamable HTTP.
> Finding IDs (`api F14` …) refer to [PLAN-api.md](PLAN-api.md).
> Execution order lives in
> [PLAN-implementation-order.md](PLAN-implementation-order.md)
> (phases 13–16 here; the web-on-API migration is phase 17+, after).

## Goal

Host `mvd-api` + `mvd-mcp` on the public internet so third-party apps
and LLM agents can use the analytics service:

1. REST API served over HTTPS, gated by API keys.
2. MCP served over streamable HTTP (same host), gated by the same keys.
3. A one-page portal where a user signs in with Discord and gets a key.
4. The hardening that PLAN-api.md flagged as prerequisite for any of
   the above (quota/GC, throttling, capped reads, CORS, error hygiene).

Explicitly **after** this plan (phase 17+): restructure `mvd-web`
(web A1→A2→A3) and move it from in-browser WASM to the hosted API.
The web app is first-party — it gets **one operator-issued service
key**; its users never register. That key ships in public JS, so it is
effectively an anonymous public tier: treat it as identification, not
secret, and give it its own rate-limit class (see phase 14).

## Design decisions (with rationale — veto here, not in review)

- **D1 — Auth + portal live inside `mvd-api`**, as
  `internal/authkeys` + `internal/portal`, activated by flags.
  A separate portal binary would need a shared key store across
  processes; inside mvd-api it's one binary, one port, one systemd
  unit, and the auth middleware and the store sit in the same process.
  Both features are **off by default** — no flags, no auth, exactly
  today's localhost behaviour (the non-secret Bearer *label* semantics
  of middleware.go:63-71 stay for that mode).
- **D2 — Key store is one JSON file, not a database.** Expected scale
  is tens-to-hundreds of users. A `keys.json` under `-auth-dir`,
  loaded into an in-memory map at startup, mutex-guarded, written with
  the existing atomic-write pattern (democache util.go) on every
  mutation. No SQLite/bbolt dependency. Revisit only if the file tops
  ~1 MB.
- **D3 — Keys are secrets; only hashes are stored.**
  Format `qwmvd_<base64url(32 random bytes)>` (prefix makes keys
  grep-able and self-identifying in configs). Store
  SHA-256(key) + metadata; show the full key exactly once at issuance.
  Lookup is hash-then-compare — constant-time compare, no timing side
  channel on the map key.
- **D4 — One active key per Discord user; regenerate revokes.** No
  key lists, no scopes, no expiry in v1. Service keys (for mvd-web and
  ops) are issued by a CLI subcommand, not the portal, and are flagged
  `service: true` so they can carry a different rate class.
- **D5 — Portal auth is Discord OAuth2 `identify` only.** No guilds,
  no email. Session = HMAC-signed cookie (stdlib `crypto/hmac`, no
  server-side session store), 1 h TTL, `SameSite=Lax`, `Secure`,
  `HttpOnly`. OAuth `state` param double-submits against the cookie.
  All mutations are POSTs. No JS framework — embedded static HTML/CSS
  via `embed.FS`.
- **D6 — MCP stays a separate process; HTTP mode added to `mvd-mcp`.**
  `mvd-mcp -http :8081 -api http://localhost:8080` serves
  `mcp.NewStreamableHTTPHandler` (go-sdk v1.6.0, already vendored;
  verified present). Keeps the deliberate A4 layering: the shim owns
  no analytics code and mvd-api owns the wire contract. The reverse
  proxy routes `/mcp` → :8081. Stdio mode remains the default and is
  untouched.
- **D7 — MCP auth is passthrough + one upfront check.** In HTTP mode
  the `getServer func(*http.Request)` hook builds a per-session proxy
  backend carrying the request's `Authorization` header verbatim, so
  every proxied REST call is authenticated by mvd-api (single point of
  key validation). Because the Supabase search tools don't transit
  mvd-api, the hook first validates the key against a new cheap
  `GET /v1/auth/check` (phase 14) and rejects the session with 401 if
  it fails — otherwise a keyless MCP session could still drive hub
  search. Header is captured at session init; a key revoked mid-session
  dies on the next proxied call (acceptable, document it).
- **D8 — Rate limiting keys on the API key, not the IP.** This is the
  resolution of the F15 warning that `clientIP` is XFF-spoofable: once
  every request carries a validated key, per-key token buckets
  (`golang.org/x/time/rate`, the one new dependency) are the primary
  limiter; per-IP limiting is left to the fronting proxy. Two classes:
  `user` (portal keys) and `service` (higher/looser, for mvd-web).
- **D9 — Deployment shape: one VPS, Caddy in front.** Caddy terminates
  TLS, normalizes `X-Forwarded-For` (fixes the middleware.go:53
  trust caveat by construction), routes `/mcp*` → mvd-mcp and
  everything else → mvd-api. systemd units for both binaries; the
  Discord client secret and cookie-HMAC secret arrive via environment
  (`EnvironmentFile=`), never flags (visible in `ps`) or the repo.

## Operator prerequisites (user does these, not Claude)

- Create a Discord application (Developer Portal → OAuth2), note client
  id + secret, register redirect URI
  `https://<domain>/portal/callback`.
- Pick the domain / provision the VPS + DNS. The plan's deploy phase
  writes the Caddyfile + systemd units into `deploy/` as tracked
  templates; secrets stay machine-local.

## Phase 13 — hosting-prep hardening (existing findings)

All specified in PLAN-api.md §"Aim/full-data API + democache"; this
just schedules them. One branch `phase-13`, stacked on `review`.

| Item | What | Effort |
|---|---|---|
| api F14 | Cache quota + GC: `-cache-max-bytes` (default ~20 GB), LRU-by-atime sweep of tier-1 + tier-2 when over budget; startup sweep deletes `results/v<old>/` trees ≠ CurrentSchemaVersion; `mvd-api cache prune`/`cache stats` subcommands (FOLLOWUPS ops items ride along) | M |
| api F15 (throttle half) | Weighted semaphore (`-max-parses`, default ~NumCPU/2) around download+parse; N cold demos no longer run N unbounded parses. Rate-limit half lands in phase 14 keyed on API key (D8) | M |
| api F16 | `io.LimitReader` cap (64 MB) in hubfetch on both CDN and `demo_source_url` paths; over-cap → `ErrHubUpstream` | S |
| api F17 | CORS middleware: permissive `Access-Control-Allow-Origin: *` + `Authorization` in allowed headers, OPTIONS preflight handled; API is read-only so `*` is safe, and the netlify-hosted web client needs it in phase 17 | S |
| api F19 | 5xx bodies become generic message + request id; `err.Error()` (cache paths, upstream URLs) goes to the log only, keyed by the same id | S |
| nits batch | The still-open PLAN-api.md nits that touch files this phase edits anyway: POST cache headers (handlers.go:173), `setCacheHeaders` on error paths, `/los` null players, `csvSetLower` move + `ciGet` on map entities | S |

Gate: `make test` green; manual: fill cache past budget → GC observed;
cold-parse storm bounded (log shows queueing); browser fetch from a
foreign origin succeeds.

## Phase 14 — API keys + auth middleware (`internal/authkeys`)

Branch `phase-14` off `phase-13`.

- Store (D2/D3): `authkeys.Store` — load/save `keys.json`, records
  `{keyHash, discordID, discordName, service, note, created, revoked}`;
  `Issue`, `Revoke`, `Lookup` (returns the record for a presented key
  or `ErrUnknownKey`). Unit tests incl. concurrent Issue/Lookup race.
- Middleware: when `-auth-dir` is set, everything under `/v1/` and
  `POST /v1/demos/{id}` requires `Authorization: Bearer qwmvd_…`;
  401 + `WWW-Authenticate: Bearer` otherwise. Exempt: `/healthz`,
  `/v1/version`, `/portal/*`. Access log's `label` field becomes the
  key's note/Discord name (never the key or its hash prefix… the hash
  prefix is fine; never the key).
- `GET /v1/auth/check` → 204 for a valid key (D7 consumer; also a
  curl-able "is my key live" for users). Exempt from nothing — it *is*
  the auth check.
- Per-key rate limit (D8): token bucket per keyHash, `user` vs
  `service` classes, flags for rate/burst; 429 + `Retry-After`.
  This closes api F15's rate-limit half.
- CLI: `mvd-api keys issue -auth-dir … -service -note "mvd-web"`,
  `keys revoke`, `keys list` (prefix + metadata only).
- Docs (lock-step rule): API.md gains an Authentication section +
  401/429 rows in the §2.4 error table; README flag table; the Bearer
  *label* paragraph rewritten to describe both modes. RELEASE_NOTES
  entry (no schema bump — transport-level change only).

Gate: `make test`; manual: no `-auth-dir` → today's behaviour
byte-identical; with it → 401 without key, 200 with, 429 past burst.

## Phase 15 — Discord portal (`internal/portal`)

Branch `phase-15` off `phase-14`.

- Flags/env: `-portal` (enable), `-portal-base-url`
  (`https://<domain>`), `DISCORD_CLIENT_ID`, `DISCORD_CLIENT_SECRET`,
  `PORTAL_COOKIE_SECRET` (HMAC key). Refuse to start the portal with
  any missing.
- Routes: `GET /portal` (landing: what the service is, docs links,
  "Sign in with Discord"); `GET /portal/login` (302 to Discord
  authorize URL, state nonce in cookie); `GET /portal/callback`
  (verify state, exchange code, `GET /users/@me` with `identify`
  scope, set signed session cookie, 302 to `/portal/key`);
  `GET /portal/key` (shows key prefix + created date, or the
  generate button); `POST /portal/key` (issue — revokes any prior key
  for this Discord id, renders the full key once); `POST /portal/logout`.
- OAuth exchange over `net/http` directly (two POST/GET calls) — no
  OAuth library.
- Templates + CSS in `internal/portal/static/` via `embed.FS`;
  visual style can crib the mvd-web palette but stays dependency-free.
- Tests: state/cookie round-trip, callback with a stubbed Discord
  server (httptest), regenerate-revokes-old invariant.
- Docs: README "getting a key" section; API.md links the portal.

Gate: full flow against the real Discord app on a dev redirect URI
(`http://localhost:8080/portal/callback` registered as a second URI);
key from the portal works against `/v1/auth/check`.

## Phase 16 — MCP over streamable HTTP

Branch `phase-16` off `phase-15`.

- `mvd-mcp -http :8081`: serve `mcp.NewStreamableHTTPHandler(getServer,
  …)`; each session's proxy backend forwards the captured
  `Authorization` header (D7) instead of `-label`; the `getServer`
  hook pre-validates against `/v1/auth/check` and returns nil/401 on
  failure. Stdio path untouched; `-http` and stdio are exclusive.
- Timeouts/shutdown mirror serve.go's pattern (Read/Write/Idle,
  SIGTERM drain).
- Decide-and-document: `Stateless` mode on (each POST self-contained,
  no session resumption) — simpler behind a proxy, and the shim holds
  no per-session state anyway. If the SDK's stateless mode fights the
  supabase check-once-per-session caching, check per request (it's a
  204 with keep-alive — cheap) and note it.
- Docs: mvd-mcp README hosted-mode section + client config snippet
  (`"url": "https://<domain>/mcp"`, `Authorization: Bearer qwmvd_…`),
  CLAUDE_DESKTOP.md hosted variant; top-level README architecture
  blurb (the service is now four surfaces: REST, MCP-stdio, MCP-HTTP,
  web).
- The REST-only/MCP asymmetry nit (PLAN-api.md: `/los`, `/shots`,
  `/streams/*`, `/airgibs` have no tools) — do the cheap half here:
  document the omissions in both READMEs; adding tools stays deferred.

Gate: `make test`; manual: MCP Inspector (or Claude Code `claude mcp
add --transport http`) against localhost `-http` with a real key →
tools list + a `getOverview` call succeed; bad key → 401 at init.

## Phase 16b — deploy (no code, tracked templates)

`deploy/Caddyfile`, `deploy/mvd-api.service`, `deploy/mvd-mcp.service`,
`deploy/README.md` (provisioning runbook: user creates the Discord
app + DNS per "Operator prerequisites"; binaries via `make build`;
`EnvironmentFile=/etc/mvd/secrets.env`). Caddy: TLS, `/mcp*` → :8081,
rest → :8080, sets real client IP. Smoke-test checklist in the runbook
(healthz, portal flow, keyed REST call, MCP init, 429 behaviour).

## What this plan deliberately defers

- **Web A1→A3 + WASM→API migration** — phase 17+, its own plan pass;
  this plan only guarantees its prerequisites (CORS, service key).
- Key scopes, expiry, multiple keys per user, usage dashboards —
  YAGNI at this scale; the store schema (D2) leaves room.
- MCP tools for `/los`, `/shots`, `/streams/*`, `/airgibs` —
  documented as absent (phase 16); add on demand.
- Per-IP limiting in-process — the fronting proxy's job (D9).
- Billing/quotas beyond rate classes — not a goal.
