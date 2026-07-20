// Package assets installs immutable runtime helpers embedded in the panel binary.
package assets

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed files/**
var files embed.FS

var directories = []struct {
	path string
	mode os.FileMode
}{
	{"etc", 0o755},
	{"etc/systemd", 0o755},
	{"etc/systemd/system", 0o755},
	{"usr", 0o755},
	{"usr/lib", 0o755},
	{"usr/lib/olcrtc-panel", 0o755},
	{"usr/lib/olcrtc-panel/wb", 0o755},
}

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
	for _, directory := range directories {
		target := filepath.Join(root, filepath.FromSlash(directory.path))
		resolved, err := filepath.Abs(target)
		if err != nil || !pathWithinRoot(root, resolved) {
			return fmt.Errorf("asset directory escapes root: %s", target)
		}
		if err := os.MkdirAll(resolved, directory.mode); err != nil {
			return fmt.Errorf("create asset directory %s: %w", target, err)
		}
		info, err := os.Lstat(resolved)
		if err != nil {
			return fmt.Errorf("inspect asset directory %s: %w", target, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("asset directory is not a real directory: %s", target)
		}
		if err := os.Chmod(resolved, directory.mode); err != nil {
			return fmt.Errorf("set asset directory mode %s: %w", target, err)
		}
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
	if err := repairExistingWBRuntime(root); err != nil {
		return err
	}
	return nil
}

// RefreshWBAutomation rewrites the WB worker, runner and service from the
// current binary, then refreshes the installed worker beside Playwright. It is
// safe to call before every browser session and keeps existing node_modules and
// Chromium downloads intact.
func RefreshWBAutomation(root string) error {
	if root == "" {
		return fmt.Errorf("asset root is empty")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, source := range []string{
		"files/systemd/olcrtc-wb-session.service",
		"files/wb/worker.mjs",
		"files/wb/run-session.sh",
	} {
		destination := destinations[source]
		data, err := fs.ReadFile(files, source)
		if err != nil {
			return fmt.Errorf("read embedded WB asset %s: %w", source, err)
		}
		target := filepath.Join(root, filepath.FromSlash(destination.path))
		resolved, err := filepath.Abs(target)
		if err != nil || !pathWithinRoot(root, resolved) {
			return fmt.Errorf("WB asset target escapes root: %s", target)
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return err
		}
		if err := atomicWrite(resolved, data, destination.mode); err != nil {
			return fmt.Errorf("refresh WB asset %s: %w", target, err)
		}
	}
	runtimeDir := filepath.Join(root, filepath.FromSlash("opt/olcrtc-panel/wb"))
	resolved, err := filepath.Abs(runtimeDir)
	if err != nil || !pathWithinRoot(root, resolved) {
		return fmt.Errorf("WB runtime escapes root: %s", runtimeDir)
	}
	info, err := os.Lstat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect WB runtime %s: %w", runtimeDir, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("WB runtime is not a real directory: %s", runtimeDir)
	}
	worker, err := fs.ReadFile(files, "files/wb/worker.mjs")
	if err != nil {
		return fmt.Errorf("read embedded WB worker: %w", err)
	}
	if err := atomicWrite(filepath.Join(resolved, "worker.mjs"), worker, 0o644); err != nil {
		return fmt.Errorf("refresh WB runtime worker: %w", err)
	}
	return nil
}

func repairExistingWBRuntime(root string) error {
	runtimeDir := filepath.Join(root, filepath.FromSlash("opt/olcrtc-panel/wb"))
	resolved, err := filepath.Abs(runtimeDir)
	if err != nil || !pathWithinRoot(root, resolved) {
		return fmt.Errorf("WB runtime escapes root: %s", runtimeDir)
	}
	info, err := os.Lstat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect WB runtime %s: %w", runtimeDir, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("WB runtime is not a real directory: %s", runtimeDir)
	}
	worker, err := fs.ReadFile(files, "files/wb/worker.mjs")
	if err != nil {
		return fmt.Errorf("read embedded WB worker: %w", err)
	}
	if err := atomicWrite(filepath.Join(resolved, "worker.mjs"), worker, 0o644); err != nil {
		return fmt.Errorf("refresh WB runtime worker: %w", err)
	}
	if err := filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		mode := entryInfo.Mode().Perm()
		switch {
		case entry.IsDir():
			mode |= 0o555
		case entryInfo.Mode().IsRegular():
			mode |= 0o444
			if entryInfo.Mode().Perm()&0o111 != 0 {
				mode |= 0o111
			}
		default:
			return nil
		}
		mode &^= 0o022
		return os.Chmod(path, mode)
	}); err != nil {
		return fmt.Errorf("repair WB runtime permissions: %w", err)
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
