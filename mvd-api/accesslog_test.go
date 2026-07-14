package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestAccessLogIdentityFields pins the access log's identity fields.
//
// The regression this guards: logIdentity prefers the key's note, and the
// portal stamps note="portal" on EVERY key it issues (portal/handlers.go), so
// `label` collapses to "portal" for every portal user and the Discord name
// appears nowhere in the line. The separate `discord` + `key` fields are what
// keep a caller identifiable regardless of notes — assert a note does not mask
// them, and that two portal users are distinguishable from each other.
func TestAccessLogIdentityFields(t *testing.T) {
	srv, auth, buf := newAuthTestServer(t, &fakeStore{})

	// Exactly what the portal mints: a Discord identity with note="portal".
	keyA, recA, err := auth.store.Issue("111", "nexusga", false, "portal")
	if err != nil {
		t.Fatal(err)
	}
	keyB, recB, err := auth.store.Issue("222", "someoneelse", false, "portal")
	if err != nil {
		t.Fatal(err)
	}

	get(t, srv.URL+"/v1/auth/check", keyA, http.StatusNoContent)
	get(t, srv.URL+"/v1/auth/check", keyB, http.StatusNoContent)

	logs := buf.String()

	// The note masks `label` for both — that is the existing (intended)
	// behaviour, and precisely why discord/key have to exist.
	if strings.Count(logs, "label=portal") != 2 {
		t.Errorf("expected both portal keys to log label=portal; got:\n%s", logs)
	}

	// ...but each caller is still identifiable, and distinguishable.
	for _, want := range []string{
		"discord=nexusga",
		"discord=someoneelse",
		"key=" + recA.HashPrefix(),
		"key=" + recB.HashPrefix(),
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("access log missing %q; got:\n%s", want, logs)
		}
	}

	// The key prefix is the join back to `keys list` — 8 hex chars, never the
	// full hash and never the key itself.
	if len(recA.HashPrefix()) != 8 {
		t.Errorf("HashPrefix() = %q; want 8 chars", recA.HashPrefix())
	}
	for _, secret := range []string{keyA, keyB, recA.KeyHash, recB.KeyHash} {
		if strings.Contains(logs, secret) {
			t.Fatalf("access log leaked a key or full hash:\n%s", logs)
		}
	}
}

// TestAccessLogIdentityEmptyWithoutAuth: an auth-exempt path and a rejected
// request carry no identity — the auth middleware never resolved a Record, so
// discord/key must be empty rather than guessing from the request.
func TestAccessLogIdentityEmptyWithoutAuth(t *testing.T) {
	srv, auth, buf := newAuthTestServer(t, &fakeStore{})
	key, _, err := auth.store.Issue("111", "nexusga", false, "portal")
	if err != nil {
		t.Fatal(err)
	}

	// /v1/version is auth-exempt: the key is never looked at.
	get(t, srv.URL+"/v1/version", key, http.StatusOK)
	// No key at all on a protected path.
	get(t, srv.URL+"/v1/auth/check", "", http.StatusUnauthorized)

	logs := buf.String()
	if strings.Contains(logs, "discord=nexusga") {
		t.Errorf("exempt/unauthenticated request must not be attributed:\n%s", logs)
	}
	if !strings.Contains(logs, `discord=""`) || !strings.Contains(logs, `key=""`) {
		t.Errorf("expected empty discord/key on unattributed requests; got:\n%s", logs)
	}
}

// get issues a GET with an optional bearer key and asserts the status.
func get(t *testing.T, url, key string, want int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s: status = %d; want %d", url, resp.StatusCode, want)
	}
}
