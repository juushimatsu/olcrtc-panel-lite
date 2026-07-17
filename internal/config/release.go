package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// InstalledRelease is the trusted metadata stored beside an installed bundle.
type InstalledRelease struct {
	BundleID     string `json:"bundle_id"`
	PanelVersion string `json:"panel_version"`
	UpstreamSHA  string `json:"upstream_sha"`
	BuildTime    string `json:"build_time"`
}

// ReadInstalledRelease reads metadata for the bundle selected by the current symlink.
func ReadInstalledRelease(releaseDir string) (InstalledRelease, error) {
	b, err := os.ReadFile(filepath.Join(releaseDir, "current", "manifest.json"))
	if err != nil {
		return InstalledRelease{}, err
	}
	var release InstalledRelease
	if err := json.Unmarshal(b, &release); err != nil {
		return InstalledRelease{}, err
	}
	return release, nil
}

// ApplyInstalledRelease makes runtime status reflect the selected bundle, not stale YAML metadata.
func ApplyInstalledRelease(cfg *Config) {
	release, err := ReadInstalledRelease(cfg.ReleaseDir)
	if err != nil {
		return
	}
	if release.PanelVersion != "" {
		cfg.PanelVersion = release.PanelVersion
	}
	if release.UpstreamSHA != "" {
		cfg.UpstreamSHA = release.UpstreamSHA
	}
}
