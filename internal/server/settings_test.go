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
