# Plan: playlists as a description, then music

Two parts. **Part 1** splits what `POST /v1/playlist-links` does today into
a client-neutral *playlist description* that mvd-api resolves from the
demos, and the *web link*, which is one client's rendering of it. **Part
2** adds Creative Commons music to the description: one track for the
whole playlist, or one per section.

## Order (owner, 2026-11-07)

Step 0 (demos by hash) is done. Music comes next, for the API, the MCP
and the light player only — the full analyzer's playlist editing gets it
later. It goes into the current link format (v2 plus a music table and
cue rows, below), so Part 1's readable description stays a separate,
later change.

## Owner decisions (2026-11-07)

- **The API describes the playlist; the link is the client's.** mvd-api
  resolves a playlist from the demos it holds. How a client opens one —
  the web player's `#playlist=` link — is a client's format. Music lives
  in the playlist, so its search is part of playlists too, not a general
  API feature.
- **Music:** one track for the whole playlist, or one per section.
- **The site is non-commercial** (no income, now or later), so NC
  licenses are fine.
- **A playlist with music plays it by default.** The viewer can turn it
  off.
- **Link length:** the readable description is accepted as the link
  payload. Shortening (server-stored short ids) or a cap on list length
  can come later.
- **Keep `gameId`** next to `demo` in the API and the MCP, for good:
  every link shared so far names gameIds.
- **Demos by sha.** A playlist names its demos by sha, gameId only
  where the hub has one. This is plan-hash-links.md's direction (branch
  `hash-links`; its phases A and C), and this plan's v3 **is** that plan's
  "playlists v3" — one new format, not two.

## Part 1 — the playlist description

### Today

`POST /v1/playlist-links` (`mvd-api/playlist.go`) resolves the request —
gameId + match ms → demo seconds through the demo's `demoOffset`, a player
name → userid, the demo's title — then packs the web player's compact
rows (v2) and answers with the URL. The same packing lives in
`mvd-web/src/lib/playlist.js` (`encodePlaylist` / `decodePlaylist`). The
two must be kept in step by hand.

### After

- **`POST /v1/playlists`** takes the same request and answers with the
  resolved **description** plus links:
  ```json
  { "playlist": { "version": 3, "name": "…",
                  "demos": [{ "sha": "<hex>", "gameId": 190001, "title": "…", "offset": 10.5 }],
                  "items": [{ "clip": { "demo": 0, "from": 137, "to": 146, "track": 12, "who": "bps", "label": "…", "slow": {…}, "text": [ … ] } },
                            { "card": { "ms": 2500, "bg": "#000000", "fadeIn": 400, "fadeOut": 400, "label": "…", "text": [ … ] } }] },
    "links": { "web": "https://mvdanalyzer.com/play.html#playlist=z…" },
    "length": …, "clips": …, "cards": …, "demos": [ … ] }
  ```
  `playlist` is in the OpenAPI spec as its own schema (`Playlist`): the
  one shape every client reads. Times are demo seconds, `track` is a
  userid or `"auto"`, as the player runs them.
- **Demo identity:** a demo is its `sha` (sha256 of the demo file, as the
  hub's CDN names it); `gameId` is there when the hub has one, null
  otherwise (a demo of our own pool, plan-hash-links.md). The request's
  clips take `demo: "sha:<hex>" | "gameId:N"`, plain `gameId` still
  accepted (hash-links phase A). The light player fetches a demo by
  its sha (CDN `<sha>.mvd`, or the API's `/games/sha:<hex>` for the hub
  row), and uses gameId only for hub links. A local file's demo is
  named by its sha too: the receiver still loads the file, but the page
  can now tell whether the file loaded is the right one.
- **The web link carries the description itself**:
  `#playlist=z<base64url(deflate-raw(JSON of playlist))>`. Any client can
  build or read one in a few lines. The compact rows go, so Go and JS no
  longer duplicate them. `links.web` is the only web-specific part of the
  response: a small, labelled adapter (`webLink(playlist)`).
- **The page** decodes v3 into its `Clip[]`, and keeps decoding v1/v2
  links (already shared). The full analyzer's share button writes v3.
- **`POST /v1/playlist-links`**: kept as an alias answering the old
  response shape (`url`, `markdown`, …) built from the new one, so the
  deployed MCP keeps working during the lockstep release; removed
  after. The MCP's `createPlaylistLink` then calls `POST /v1/playlists`
  and returns the link (and the description, for clients that want it).

Measured (2026-11-07, deflate + base64url): the readable payload is
20–30 % longer than v2's compact rows — 10 clips 417 → 543 chars,
30 clips with cards 965 → 1275, 100 clips over 20 demos with cards and
texts 2291 → 2779. Playlist size, not the format, decides what fits a
chat message: Discord's 2000-character limit is passed by a 100-clip
link either way.

### Steps

0. Prerequisite from plan-hash-links.md phase A: `/v1/games/sha:<hex>`
   (the hub row for a sha) and demo ids in the request. Part 1 can carry
   that phase; then hash-links phase C is done by this plan.
1. Define `Playlist` in OpenAPI. Go: the resolver returns it; `webLink`
   encodes it. Tests: golden description for a corpus demo, round trip
   through the encoder.
2. Web: `decodePlaylist` reads v3 into `Clip[]`; `encodePlaylist` writes
   v3 from the same description shape (one JS function builds it from
   `Clip[]`). Tests: v1/v2/v3 decode, and the Go-encoded link decoding to
   the same clips (a shared fixture).
3. API: `POST /v1/playlists`, `POST /v1/playlist-links` as the alias,
   with the drift test and the docs (API.md recipe, OpenAPI).
4. MCP: `createPlaylistLink` calls the new endpoint; README.

## Part 2 — music

### Licenses

| License | Allowed | Why |
|---|---|---|
| CC0, CC BY, CC BY-NC | yes | credit shown |
| CC BY-SA, CC BY-NC-SA | yes | credit shown, and the playlist marked as shared under the same license |
| any **ND** | **no** | CC 4.0 §1(a): music "synched in timed relation with a moving image" is always Adapted Material, which ND forbids. A playlist is exactly that. |

ShareAlike: the synced playlist is an adaptation, so it is offered under
the track's SA license. The credit line says so. The clips are game
demos, not our work, so this costs nothing.

The credit gives title, artist, source and license, each linked (CC's
TASL). It shows under the player while the track plays, and in the clip
list.

### Sources

Checked from this machine 2026-11-07:

- **ccMixter**, `https://ccmixter.org/api/query?f=json&lic=by&tags=…`:
  an open JSON API (no key). Filters by license, tags and tempo; results
  carry artist, license URL, file page. The main search source.
- **Wikimedia Commons**, `w/api.php`, `filetype:audio`: open (send a
  User-Agent). Results carry `LicenseShortName` and `Artist`. Smaller and
  of mixed quality; files are often WAV/OGG, which are large, so prefer
  Commons' transcodes.
- **Incompetech** (Kevin MacLeod, all CC BY 4.0): MP3s served directly
  (HTTP 200), no API. A curated default list in the repo, with title,
  mood and length.
- **Jamendo**: an API with a client_id, mostly NC tracks, and usable
  given the NC decision. Later, if ccMixter is too thin.
- **Openverse** (aggregates the above): its API answers server-side calls
  with a Cloudflare challenge. Not usable from mvd-api.

### In the description: a music cue is an item

A third kind of item, `{ "music": { … } }`, takes no time: from it on, its
track plays. `{ "music": null }` means silence from there.

- **Whole playlist:** one cue first.
- **Per section:** a cue at each section's start (typically right
  before its title card).

The resolved cue carries everything a client needs to play it and give
credit:

```json
{ "music": { "source": "ccmixter", "id": "16626", "url": "https://…mp3",
             "title": "…", "artist": "…", "license": "CC BY 3.0",
             "licenseUrl": "…", "page": "…", "durationSec": 214, "shareAlike": false } }
```

### API and MCP

- **`GET /v1/playlists/music?q=&tags=&minSec=&maxSec=&source=`:** a search
  for a track to cue in a playlist. It proxies ccMixter and Commons, plus
  the Incompetech list. ND is never returned. Results are cached briefly
  and rate-limited per key, like the other proxied calls. It sits under
  the playlists tag and docs section.
- **`POST /v1/playlists` takes `{ "music": { "source": …, "id": … } }`
  items** (or `{ "music": null }`) — **not a URL**. mvd-api looks the
  track up at its source and fills in the cue (credit, file URL,
  license). So the license shown comes from the source, not from a
  claim in the request, and a link cannot make the page play arbitrary
  audio. An unknown track or an ND one is a 400.
- **MCP:** `findPlaylistMusic` ("a track for createPlaylistLink's music
  items"), and the music item in `createPlaylistLink`'s input.

### Page

- The music plays in an `<audio>` element: cross-origin playback needs
  no CORS. It starts on the run's first click (the gesture the page
  already waits for) and fades out at the end of the playlist or on
  stop. Passing a cue crossfades (~1 s). Jumping to an item (a click in
  the list) plays the cue in force there: the last one before it.
- **Pause:** pausing the game pauses the music. A title card keeps it
  playing.
- **Game sound:** turned down while music plays (FTE `volume`,
  restored after). A music on / off toggle sits beside the sound
  button.
- **Credit:** under the player and in the clip list, with the
  ShareAlike line when it applies.
- **The full analyzer's Player tab:** it keeps the cues when a playlist
  opens there, and lists them. Editing them comes later.
- **Volume:** remembered per viewer (localStorage), like the other
  player settings.

### Hosting

v1 hotlinks the track's file URL. Optional later: mvd-api keeps a copy
(CC allows it), served from its own host. That protects a playlist from
a track deleted at the source, or a host that blocks hotlinking (not
checked for ccMixter).

### Steps

5. Description and web decode: the music item, with tests.
6. API: the music search and the track lookup (recorded upstream
   answers, no network in `make test`), with OpenAPI.
7. API: music items in `POST /v1/playlists`, resolved through the
   lookup.
8. MCP: `findPlaylistMusic`, and the music item in
   `createPlaylistLink`.
9. Page: the runner plays the cues, plus the audio element, ducking,
   toggle and credit.
10. Docs: READMEs, API.md, RELEASE_NOTES.

## Open

- **Duck level:** how far the game sound drops under music (start at
  30 %?).
- **Track shorter than its section:** loop it or stop. Loop with a
  crossfade?
- **Editing cues in the Player tab's playlist pane:** later.
- **Shortening links:** server-stored ids (ties to plan-hash-links.md),
  or a cap on items. Later.
