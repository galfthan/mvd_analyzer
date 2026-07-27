package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-api/internal/democache"
)

// handleUpload: POST /v1/demos — accept a raw or gzipped MVD demo in the
// request body, store it under its content SHA, parse it synchronously, and
// return the same identity metadata handleLoad returns. The client then reads
// analysis with the existing GET /v1/demos/{demoId}/* endpoints.
//
// REST-ONLY BY DESIGN: this endpoint is deliberately NOT exposed as an MCP
// tool. mvd-mcp hand-registers its tool set (mvd-mcp/mcp_tools.go); leaving the
// upload out of that list is the exclusion mechanism. Do not add it there.
//
// PRIVACY: an uploaded demo is addressed only by the SHA-256 of its content and
// is readable by ANY key holder who knows that SHA — there is no ownership or
// ACL. It is a first-class tier-1 entry in the shared GC pool, so after
// eviction a sha: GET 404s and the client re-uploads. Both are documented in
// the openapi description and API.md.
//
// Body handling: the body is sniffed by gzip magic (raw .mvd or .mvd.gz both
// accepted); Content-Type is not enforced. No multipart — a flatter, safer
// surface with no temp-file spillover.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	limit := s.upload.maxBytes
	if limit <= 0 {
		// Uploads disabled: the route stays registered (the openapi drift test
		// pins spec↔router), but the handler 404s — mirroring the -maps-dir
		// precedent in handleMapGeometry.
		writeError(w, http.StatusNotFound, "uploads_disabled",
			"demo upload is not enabled on this server")
		return
	}

	// Cheap pre-read reject when the client advertises an over-limit body.
	if r.ContentLength > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "demo_too_large",
			fmt.Sprintf("demo exceeds the %d-byte upload limit", limit))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "demo_too_large",
				fmt.Sprintf("demo exceeds the %d-byte upload limit", limit))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_param", "could not read request body")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param", "empty request body")
		return
	}

	// Per-key daily quota (auth mode only — no key identity in localhost mode,
	// so the quota is skipped there). Charged on the accepted bytes before the
	// expensive store+parse, so a throttled key never occupies a parse slot.
	if keyHash := uploadKeyHash(r.Context()); keyHash != "" {
		if ok, retry := s.uploadLedger.charge(keyHash, int64(len(body)),
			s.upload.dailyBytes, s.upload.dailyCount); !ok {
			secs := int(retry / time.Second)
			if time.Duration(secs)*time.Second < retry {
				secs++
			}
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeError(w, http.StatusTooManyRequests, "upload_quota_exceeded",
				"daily upload quota exceeded")
			return
		}
	}

	sha, _, err := s.store.PutDemo(r.Context(), body)
	if err != nil {
		switch {
		case errors.Is(err, democache.ErrDemoTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "demo_too_large",
				"demo decompresses to more than the allowed size")
		case errors.Is(err, democache.ErrInvalidGzip):
			writeError(w, http.StatusBadRequest, "invalid_gzip",
				"body has a gzip header but is not a valid gzip stream")
		default:
			s.writeInternal(w, r, err)
		}
		return
	}

	// Parse synchronously under the existing semaphore + singleflight. An
	// immediate 422 on unparseable input beats a deferred 500 on the first GET.
	id := democache.DemoID{Kind: "sha256", SHA: sha}
	res, meta, err := s.store.GetResult(r.Context(), id)
	if err != nil {
		if errors.Is(err, democache.ErrParse) {
			// Anti-smuggling: an unparseable body must not linger as
			// content-addressed storage. Best-effort remove tier-1 + tier-2.
			s.store.RemoveDemo(sha)
			writeError(w, http.StatusUnprocessableEntity, "unparseable_demo",
				"the uploaded bytes are not a parseable MVD demo")
			return
		}
		s.mapStoreError(w, r, err)
		return
	}
	// Degenerate-Result gate: a body that parses but contains no actual game
	// (no ServerData / zero players / zero events) is rejected the same way, so
	// arbitrary bytes that happen to decode cannot be smuggled into storage.
	// A truncated-but-real demo (partial Result with Errors) still passes.
	if degenerateResult(res) {
		s.store.RemoveDemo(sha)
		writeError(w, http.StatusUnprocessableEntity, "unparseable_demo",
			"the uploaded demo contains no analyzable game (no server data / players / events)")
		return
	}

	s.logger.Info("demo uploaded",
		"request_id", requestID(r.Context()), "sha", meta.SHA256, "cache", cacheState(meta))

	// Success mirrors handleLoad exactly: a POST is not a cacheable resource,
	// so no Cache-Control/ETag — just the informational tier + schema headers.
	w.Header().Set("X-Schema-Version", fmt.Sprintf("%d", meta.SchemaVersion))
	w.Header().Set("X-Cache", cacheState(meta))
	writeJSON(w, http.StatusOK, map[string]any{
		"demoId":        "sha:" + meta.SHA256,
		"sha256":        meta.SHA256,
		"fromCache":     meta.FromCache,
		"schemaVersion": meta.SchemaVersion,
	})
}

// uploadKeyHash returns the authenticated key's hash for the request, or "" in
// no-auth mode (no key identity). It reads the same *reqInfo the auth
// middleware writes its log identity into.
func uploadKeyHash(ctx context.Context) string {
	if info := reqInfoFrom(ctx); info != nil {
		return info.keyHash
	}
	return ""
}

// degenerateResult reports whether a parsed Result represents no actual game —
// the upload parse-gate's second check (past a hard ErrParse). It is
// intentionally strict: a real demo, even a truncated one, carries a
// svc_serverdata (map + gamedir), a scoreboard, and some events.
func degenerateResult(res *result.Result) bool {
	if res == nil {
		return true
	}
	// No map at all: Match.Map is the canonical shortname, resolved from the
	// demoinfo block, then the serverinfo `map` key, then the svc_serverdata
	// level title. Empty means not one of those three named a level, which
	// no real recording manages — there is no game to analyze.
	if res.Match == nil || res.Match.Map == "" {
		return true
	}
	// Zero players.
	if len(res.Match.Players) == 0 {
		return true
	}
	// Zero events: no frags and no messages — nothing happened.
	events := 0
	if res.Messages != nil {
		events += len(res.Messages.Events)
	}
	if res.Frags != nil {
		events += len(res.Frags.Frags)
	}
	return events == 0
}
