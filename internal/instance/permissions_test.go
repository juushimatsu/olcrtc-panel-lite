package instance

import (
	"path/filepath"
	"testing"
)

func TestPermissionPathWithin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "releases")
	if !pathWithin(root, filepath.Join(root, "bundle", "olcrtc")) {
		t.Fatal("bundle binary was rejected")
	}
	if pathWithin(root, filepath.Join(filepath.Dir(root), "other", "olcrtc")) {
		t.Fatal("path outside release directory was accepted")
	}
}
