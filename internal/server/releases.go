package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
)

type releaseListItem struct {
	BundleID    string    `json:"bundle_id"`
	Name        string    `json:"name"`
	PublishedAt time.Time `json:"published_at"`
	URL         string    `json:"url"`
	Latest      bool      `json:"latest"`
	Current     bool      `json:"current"`
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

func (s *Server) writeUpdatesReleases(w http.ResponseWriter, r *http.Request) {
	current := config.InstalledRelease{PanelVersion: s.cfg.PanelVersion, UpstreamSHA: s.cfg.UpstreamSHA}
	if installed, err := config.ReadInstalledRelease(s.cfg.ReleaseDir); err == nil {
		current = installed
	}
	response := map[string]any{
		"configured":         false,
		"current":            current,
		"rollback_available": fileExists(filepath.Join(s.cfg.ReleaseDir, "previous", "olcrtc-panel")),
		"items":              []releaseListItem{},
	}
	apiURL, ok := githubReleasesURL(s.cfg.ReleaseManifestURL)
	if !ok {
		writeJSON(w, http.StatusOK, response)
		return
	}
	response["configured"] = true
	items, err := fetchGitHubReleases(r.Context(), &http.Client{Timeout: 15 * time.Second}, apiURL, current.BundleID)
	if err != nil {
		response["error"] = "Не удалось получить список релизов"
		writeJSON(w, http.StatusOK, response)
		return
	}
	response["items"] = items
	writeJSON(w, http.StatusOK, response)
}

func githubReleasesURL(manifestURL string) (string, bool) {
	parsed, err := url.Parse(manifestURL)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] == "" || parts[2] != "releases" {
		return "", false
	}
	return "https://api.github.com/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/releases?per_page=10", true
}

func fetchGitHubReleases(ctx context.Context, client *http.Client, apiURL, currentBundle string) ([]releaseListItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "olcrtc-panel-lite")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub releases returned HTTP %d", resp.StatusCode)
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&releases); err != nil {
		return nil, err
	}
	items := make([]releaseListItem, 0, 10)
	for _, release := range releases {
		if release.Draft || release.Prerelease || !bundlePattern.MatchString(release.TagName) {
			continue
		}
		name := release.Name
		if name == "" {
			name = release.TagName
		}
		items = append(items, releaseListItem{
			BundleID:    release.TagName,
			Name:        name,
			PublishedAt: release.PublishedAt,
			URL:         release.HTMLURL,
			Latest:      len(items) == 0,
			Current:     release.TagName == currentBundle,
		})
		if len(items) == 10 {
			break
		}
	}
	return items, nil
}
