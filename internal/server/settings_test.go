package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestGitHubReleasesURL(t *testing.T) {
	got, ok := githubReleasesURL("https://github.com/owner/repository/releases/latest/download/manifest.json")
	if !ok || got != "https://api.github.com/repos/owner/repository/releases?per_page=10" {
		t.Fatalf("githubReleasesURL = %q, %v", got, ok)
	}
	if _, ok := githubReleasesURL("https://example.com/releases/latest/manifest.json"); ok {
		t.Fatal("non-GitHub URL accepted")
	}
}

func TestFetchGitHubReleases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"bundle-new","name":"Newest","html_url":"https://example/new","published_at":"2026-07-17T00:00:00Z","draft":false},
			{"tag_name":"bundle-current","name":"Current","html_url":"https://example/current","published_at":"2026-07-16T00:00:00Z","draft":false},
			{"tag_name":"invalid tag","published_at":"2026-07-15T00:00:00Z","draft":false}
		]`))
	}))
	defer server.Close()
	items, err := fetchGitHubReleases(context.Background(), server.Client(), server.URL, "bundle-current")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].Latest || !items[1].Current {
		t.Fatalf("unexpected releases: %#v", items)
	}
}

func TestFetchGitHubReleasesMarksNewestPublishedReleaseLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"bundle-old","name":"Older","html_url":"https://example/old","published_at":"2026-07-16T00:00:00Z","draft":false},
			{"tag_name":"bundle-new","name":"Newest","html_url":"https://example/new","published_at":"2026-07-18T00:00:00Z","draft":false},
			{"tag_name":"bundle-middle","name":"Middle","html_url":"https://example/middle","published_at":"2026-07-17T00:00:00Z","draft":false}
		]`))
	}))
	defer server.Close()

	items, err := fetchGitHubReleases(context.Background(), server.Client(), server.URL, "bundle-middle")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].BundleID != "bundle-new" || !items[0].Latest || items[1].BundleID != "bundle-middle" || !items[1].Current {
		t.Fatalf("unexpected release order or markers: %#v", items)
	}
	for _, item := range items[1:] {
		if item.Latest {
			t.Fatalf("older release marked latest: %#v", item)
		}
	}
}

func TestOperationProgressFromStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := []byte(`{"phase":"browser","message":"Installing Chromium","percent":75,"updated_at":` + strconv.FormatInt(time.Now().Unix(), 10) + `}`)
	if err := os.WriteFile(path, state, 0o600); err != nil {
		t.Fatal(err)
	}
	progress := operationProgressFrom(operationState{State: "idle"}, path)
	if progress.State != "running" || progress.Phase != "browser" || progress.Percent != 75 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
}

func TestOperationProgressIgnoresStaleStateForNewRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := []byte(`{"phase":"error","message":"Old failure","percent":0,"updated_at":1}`)
	if err := os.WriteFile(path, state, 0o600); err != nil {
		t.Fatal(err)
	}
	progress := operationProgressFrom(operationState{State: "completed", StartedAt: time.Now()}, path)
	if progress.State != "completed" || progress.Phase != "" || progress.Percent != 100 {
		t.Fatalf("stale state affected new operation: %#v", progress)
	}
}

func TestSanitizeWBSessionStateNeverReturnsCapturedToken(t *testing.T) {
	state := sanitizeWBSessionStateForResponse(map[string]any{
		"phase": "success", "message": "captured", "percent": 100, "token": "secret-bearer",
	})
	if _, exists := state["token"]; exists {
		t.Fatal("captured WB token was returned in session state")
	}
	if state["phase"] != "applying" || state["percent"] != 95 {
		t.Fatalf("state was not held in applying phase: %#v", state)
	}
}

func TestWBCreateSessionExposesTokenOnlyAfterSuccess(t *testing.T) {
	if !shouldExposeWBCreateToken(map[string]any{"phase": "success", "action": "create"}) {
		t.Fatal("successful create session did not allow one authenticated token response")
	}
	for _, state := range []map[string]any{
		{"phase": "applying", "action": "create"},
		{"phase": "success", "action": "refresh"},
		{"phase": "error", "action": "create"},
	} {
		if shouldExposeWBCreateToken(state) {
			t.Fatalf("token was exposed for state %#v", state)
		}
	}
}

func TestTelemostCreateSessionNeverExposesStoredWBToken(t *testing.T) {
	state := map[string]any{"phase": "success", "action": "create", "provider": "telemost", "room_id": "01234567890123"}
	if shouldExposeWBCreateToken(state) {
		t.Fatal("Telemost create session was allowed to expose the stored WB token")
	}
}

func TestNormalizeAutomationProxy(t *testing.T) {
	for _, test := range []struct {
		name     string
		mode     string
		address  string
		username string
		wantMode string
		wantAddr string
	}{
		{name: "direct", mode: " direct ", wantMode: "direct"},
		{name: "http", mode: "HTTP", address: "proxy.example:8080", username: " user ", wantMode: "http", wantAddr: "proxy.example:8080"},
		{name: "socks IPv4", mode: "socks5", address: "127.0.0.1:1080", wantMode: "socks5", wantAddr: "127.0.0.1:1080"},
		{name: "socks IPv6", mode: "socks5", address: "[2001:db8::1]:1080", wantMode: "socks5", wantAddr: "[2001:db8::1]:1080"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeAutomationProxy(test.mode, test.address, test.username)
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != test.wantMode || got.Address != test.wantAddr {
				t.Fatalf("proxy = %#v", got)
			}
		})
	}

	for _, test := range []struct {
		name     string
		mode     string
		address  string
		username string
	}{
		{name: "unknown mode", mode: "ftp", address: "proxy.example:21"},
		{name: "missing address", mode: "http"},
		{name: "embedded scheme", mode: "http", address: "http://proxy.example:8080"},
		{name: "missing port", mode: "https", address: "proxy.example"},
		{name: "invalid port", mode: "socks5", address: "proxy.example:70000"},
		{name: "path", mode: "http", address: "proxy.example:8080/path"},
		{name: "username newline", mode: "http", address: "proxy.example:8080", username: "user\nname"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeAutomationProxy(test.mode, test.address, test.username); err == nil {
				t.Fatal("invalid proxy was accepted")
			}
		})
	}
}
