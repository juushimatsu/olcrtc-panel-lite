package instance

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
)

// PreparePermissions repairs the exact filesystem paths required by one systemd instance.
// It is intended for the root ExecStartPre command in olcrtc-instance@.service.
func PreparePermissions(cfg config.Config, id int64) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if id < 1 {
		return errors.New("instance ID must be positive")
	}
	for name, path := range map[string]string{
		"instances directory": cfg.InstancesDir,
		"runtime directory":   cfg.RuntimeDir,
		"release directory":   cfg.ReleaseDir,
		"olcrtc binary":       cfg.OlcrtcBinary,
	} {
		if path == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("%s must be an absolute path", name)
		}
	}

	account, err := user.Lookup("olcrtc")
	if err != nil {
		return fmt.Errorf("lookup olcrtc account: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse olcrtc uid: %w", err)
	}
	group, err := user.LookupGroup("olcrtc")
	if err != nil {
		return fmt.Errorf("lookup olcrtc group: %w", err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("parse olcrtc gid: %w", err)
	}

	instanceDir := filepath.Join(cfg.InstancesDir, strconv.FormatInt(id, 10))
	runtimeDir := filepath.Join(cfg.RuntimeDir, strconv.FormatInt(id, 10))
	if err := os.MkdirAll(filepath.Join(runtimeDir, "data"), 0o750); err != nil {
		return fmt.Errorf("create instance runtime: %w", err)
	}

	paths := []struct {
		path string
		uid  int
		gid  int
		mode os.FileMode
	}{
		{filepath.Dir(cfg.InstancesDir), 0, gid, 0o710},
		{cfg.InstancesDir, 0, gid, 0o750},
		{instanceDir, 0, gid, 0o750},
		{filepath.Join(instanceDir, "config.yaml"), 0, gid, 0o640},
		{filepath.Join(instanceDir, "key.hex"), 0, gid, 0o640},
		{runtimeDir, uid, gid, 0o750},
		{filepath.Join(runtimeDir, "data"), uid, gid, 0o750},
		{filepath.Dir(cfg.ReleaseDir), 0, gid, 0o710},
		{cfg.ReleaseDir, 0, gid, 0o710},
	}
	for _, item := range paths {
		if err := setPermissions(item.path, item.uid, item.gid, item.mode); err != nil {
			return err
		}
	}

	binary, err := filepath.EvalSymlinks(cfg.OlcrtcBinary)
	if err != nil {
		return fmt.Errorf("resolve olcrtc binary: %w", err)
	}
	binaryInfo, err := os.Stat(binary)
	if err != nil {
		return fmt.Errorf("inspect olcrtc binary: %w", err)
	}
	if !binaryInfo.Mode().IsRegular() {
		return errors.New("olcrtc binary is not a regular file")
	}
	if pathWithin(cfg.ReleaseDir, binary) {
		if err := setPermissions(filepath.Dir(binary), 0, gid, 0o710); err != nil {
			return err
		}
	}
	if err := setPermissions(binary, 0, gid, 0o750); err != nil {
		return err
	}
	return nil
}

func setPermissions(path string, uid, gid int, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to change permissions through symlink %s", path)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
