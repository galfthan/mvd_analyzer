package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-api/internal/authkeys"
	"github.com/mvd-analyzer/mvd-api/internal/democache"
)

// gzipBytes wraps s in a gzip stream (upload request-body fixture).
func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// postDemo POSTs body to url with an optional bearer key, returning the raw
// body and status.
func postDemo(t *testing.T, url, bearer string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

// newUploadServer builds a no-auth server with a custom upload config.
func newUploadServer(t *testing.T, store demoStore, cfg uploadConfig) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(newRouter(store, logger, "", cfg, nil, nil, &fakeSearcher{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestUpload_HappyPath: a POSTed demo returns a sha: id whose overview then
// resolves.
func TestUpload_HappyPath(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(t, store)
	defer srv.Close()

	resp, body := postDemo(t, srv.URL+"/v1/demos", "", gzipBytes(t, "demo-bytes"))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; want 200 (body=%s)", resp.StatusCode, body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	demoID, _ := m["demoId"].(string)
	if len(demoID) < 4 || demoID[:4] != "sha:" {
		t.Fatalf("demoId = %q; want sha:… form", demoID)
	}
	if _, ok := m["fromCache"].(bool); !ok {
		t.Errorf("fromCache = %v; want a boolean", m["fromCache"])
	}

	// The returned id must resolve on a follow-up GET.
	over := getJSON(t, srv.URL+"/v1/demos/"+demoID+"/overview", 200)
	if over["schemaVersion"] == nil {
		t.Errorf("overview missing schemaVersion")
	}
}

// TestUpload_Disabled: -max-upload-bytes 0 → 404 uploads_disabled.
func TestUpload_Disabled(t *testing.T) {
	srv := newUploadServer(t, &fakeStore{}, uploadConfig{maxBytes: 0})
	resp, body := postDemo(t, srv.URL+"/v1/demos", "", gzipBytes(t, "x"))
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d; want 404 (body=%s)", resp.StatusCode, body)
	}
	assertCode(t, body, "uploads_disabled")
}

// TestUpload_TooLarge: an over-limit body is rejected 413 demo_too_large.
func TestUpload_TooLarge(t *testing.T) {
	srv := newUploadServer(t, &fakeStore{}, uploadConfig{maxBytes: 8})
	resp, body := postDemo(t, srv.URL+"/v1/demos", "", gzipBytes(t, "way more than eight bytes of content"))
	if resp.StatusCode != 413 {
		t.Fatalf("status = %d; want 413 (body=%s)", resp.StatusCode, body)
	}
	assertCode(t, body, "demo_too_large")
}

// TestUpload_EmptyBody: an empty body is 400 invalid_param.
func TestUpload_EmptyBody(t *testing.T) {
	srv := newTestServer(t, &fakeStore{})
	defer srv.Close()
	resp, body := postDemo(t, srv.URL+"/v1/demos", "", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d; want 400 (body=%s)", resp.StatusCode, body)
	}
	assertCode(t, body, "invalid_param")
}

// TestUpload_InvalidGzip: PutDemo's ErrInvalidGzip maps to 400 invalid_gzip.
func TestUpload_InvalidGzip(t *testing.T) {
	store := &fakeStore{putErr: democache.ErrInvalidGzip}
	srv := newTestServer(t, store)
	defer srv.Close()
	resp, body := postDemo(t, srv.URL+"/v1/demos", "", []byte{0x1f, 0x8b, 0x00, 0x01})
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d; want 400 (body=%s)", resp.StatusCode, body)
	}
	assertCode(t, body, "invalid_gzip")
}

// TestUpload_Unparseable_Cleanup: an ErrParse from GetResult is 422 and the
// tier-1/tier-2 files are removed (parse-gate anti-smuggling).
func TestUpload_Unparseable_Cleanup(t *testing.T) {
	store := &fakeStore{parseFail: true}
	srv := newTestServer(t, store)
	defer srv.Close()
	resp, body := postDemo(t, srv.URL+"/v1/demos", "", gzipBytes(t, "not-a-demo"))
	if resp.StatusCode != 422 {
		t.Fatalf("status = %d; want 422 (body=%s)", resp.StatusCode, body)
	}
	assertCode(t, body, "unparseable_demo")
	if len(store.removed) != 1 {
		t.Fatalf("RemoveDemo called %d times; want 1", len(store.removed))
	}
}

// TestUpload_Degenerate_Cleanup: a body that parses to no actual game is also
// 422 + cleanup.
func TestUpload_Degenerate_Cleanup(t *testing.T) {
	// putResult has no Match → degenerate.
	store := &fakeStore{putResult: &result.Result{SchemaVersion: result.CurrentSchemaVersion}}
	srv := newTestServer(t, store)
	defer srv.Close()
	resp, body := postDemo(t, srv.URL+"/v1/demos", "", gzipBytes(t, "empty-game"))
	if resp.StatusCode != 422 {
		t.Fatalf("status = %d; want 422 (body=%s)", resp.StatusCode, body)
	}
	assertCode(t, body, "unparseable_demo")
	if len(store.removed) != 1 {
		t.Fatalf("RemoveDemo called %d times; want 1", len(store.removed))
	}
}

// TestUpload_QuotaExceeded: the per-key daily count quota 429s the second
// upload in auth mode.
func TestUpload_QuotaExceeded(t *testing.T) {
	buf := &syncBuf{}
	logger := slog.New(slog.NewTextHandler(buf, nil))
	keys, err := authkeys.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := &authenticator{
		store:   keys,
		limiter: newKeyLimiter(rateClass{rate: 1000, burst: 1000}, rateClass{rate: 1000, burst: 1000}),
		logger:  logger,
	}
	cfg := uploadConfig{maxBytes: 64 << 20, dailyCount: 1}
	srv := httptest.NewServer(newRouter(&fakeStore{}, logger, "", cfg, auth, nil, &fakeSearcher{}))
	defer srv.Close()

	key, _, _ := auth.store.Issue("1", "u", false, "")
	body := gzipBytes(t, "quota-demo")

	resp1, b1 := postDemo(t, srv.URL+"/v1/demos", key, body)
	if resp1.StatusCode != 200 {
		t.Fatalf("first upload status = %d; want 200 (body=%s)", resp1.StatusCode, b1)
	}
	resp2, b2 := postDemo(t, srv.URL+"/v1/demos", key, body)
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second upload status = %d; want 429 (body=%s)", resp2.StatusCode, b2)
	}
	assertCode(t, b2, "upload_quota_exceeded")
	if ra := resp2.Header.Get("Retry-After"); ra == "" {
		t.Errorf("429 missing Retry-After")
	} else if n, err := strconv.Atoi(ra); err != nil || n < 1 {
		t.Errorf("Retry-After = %q; want a positive integer", ra)
	}
}

// TestUpload_Unauthorized: auth mode rejects an upload with no key (401).
func TestUpload_Unauthorized(t *testing.T) {
	srv, _, _ := newAuthTestServer(t, &fakeStore{})
	resp, body := postDemo(t, srv.URL+"/v1/demos", "", gzipBytes(t, "x"))
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d; want 401 (body=%s)", resp.StatusCode, body)
	}
	assertCode(t, body, "unauthorized")
}

// assertCode decodes an error envelope and checks its code.
func assertCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	if env.Error.Code != want {
		t.Errorf("error code = %q; want %q (body=%s)", env.Error.Code, want, body)
	}
}
