// Package assets installs immutable runtime helpers embedded in the panel binary.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed files/**
var files embed.FS

var destinations = map[string]struct {
	path string
	mode os.FileMode
}{
	"files/systemd/olcrtc-panel.service":      {"etc/systemd/system/olcrtc-panel.service", 0o644},
	"files/systemd/olcrtc-instance@.service":  {"etc/systemd/system/olcrtc-instance@.service", 0o644},
	"files/systemd/olcrtc-wb-session.service": {"etc/systemd/system/olcrtc-wb-session.service", 0o644},
	"files/wb/worker.mjs":                     {"usr/lib/olcrtc-panel/wb/worker.mjs", 0o644},
	"files/wb/run-session.sh":                 {"usr/lib/olcrtc-panel/wb/run-session.sh", 0o755},
	"files/wb/install-components.sh":          {"usr/lib/olcrtc-panel/wb/install-components.sh", 0o755},
	"files/wb/remove-components.sh":           {"usr/lib/olcrtc-panel/wb/remove-components.sh", 0o755},
	"files/update/update.sh":                  {"usr/lib/olcrtc-panel/update.sh", 0o755},
}

// Install writes the fixed asset set below root.
func Install(root string) error {
	if root == "" {
		return fmt.Errorf("asset root is empty")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for source, destination := range destinations {
		data, err := fs.ReadFile(files, source)
		if err != nil {
			return fmt.Errorf("read embedded asset %s: %w", source, err)
		}
		target := filepath.Join(root, filepath.FromSlash(destination.path))
		resolved, err := filepath.Abs(target)
		if err != nil || !pathWithinRoot(root, resolved) {
			return fmt.Errorf("asset target escapes root: %s", target)
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return err
		}
		if err := atomicWrite(resolved, data, destination.mode); err != nil {
			return fmt.Errorf("install asset %s: %w", target, err)
		}
	}
	return nil
}

func pathWithinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".asset-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	err = tmp.Chmod(mode)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(path)
		return os.Rename(tmpPath, path)
	}
	return nil
}
