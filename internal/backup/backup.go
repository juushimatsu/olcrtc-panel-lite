// Package backup creates bounded panel backup archives.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Manager creates ordinary UI backups without machine keys or plaintext tokens.
type Manager struct {
	db           *sql.DB
	instancesDir string
	backupDir    string
}

// NewManager creates a backup manager.
func NewManager(db *sql.DB, instancesDir, backupDir string) *Manager {
	return &Manager{db: db, instancesDir: instancesDir, backupDir: backupDir}
}

// Create makes a gzip tar archive with an online SQLite snapshot and redacted YAML.
func (m *Manager) Create(ctx context.Context) (string, error) {
	if err := os.MkdirAll(m.backupDir, 0o700); err != nil {
		return "", err
	}
	id := time.Now().UTC().Format("20060102T150405Z")
	work, err := os.MkdirTemp(m.backupDir, ".backup-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	dbCopy := filepath.Join(work, "panel.db")
	if _, err := m.db.ExecContext(ctx, `VACUUM INTO ?`, dbCopy); err != nil {
		return "", fmt.Errorf("SQLite online backup: %w", err)
	}
	archivePath := filepath.Join(m.backupDir, "olcrtc-panel-"+id+".tar.gz")
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(f)
	tarWriter := tar.NewWriter(gz)
	err = addFile(tarWriter, dbCopy, "panel.db", false)
	if err == nil {
		err = filepath.WalkDir(m.instancesDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Name() != "config.yaml" {
				return nil
			}
			rel, relErr := filepath.Rel(m.instancesDir, path)
			if relErr != nil {
				return relErr
			}
			return addFile(tarWriter, path, filepath.ToSlash(filepath.Join("instances", rel)), true)
		})
	}
	closeErr := tarWriter.Close()
	if err == nil {
		err = closeErr
	}
	if closeErr = gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr = f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(archivePath)
		return "", err
	}
	return archivePath, nil
}

func addFile(writer *tar.Writer, path, name string, redactYAML bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if redactYAML {
		b = redactConfig(b)
	}
	header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: time.Now()}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err = writer.Write(b)
	return err
}

func redactConfig(input []byte) []byte {
	var value map[string]any
	if yaml.Unmarshal(input, &value) != nil {
		return []byte("# config omitted: parse error\n")
	}
	if auth, ok := value["auth"].(map[string]any); ok {
		delete(auth, "token")
	}
	if socks, ok := value["socks"].(map[string]any); ok {
		delete(socks, "proxy_pass")
		delete(socks, "proxy_user")
	}
	b, err := yaml.Marshal(value)
	if err != nil {
		return []byte("# config omitted: render error\n")
	}
	return b
}

// Resolve returns a backup path only when ID has the expected safe format.
func (m *Manager) Resolve(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\\`) || !strings.HasPrefix(id, "olcrtc-panel-") || !strings.HasSuffix(id, ".tar.gz") {
		return "", fmt.Errorf("invalid backup ID")
	}
	path := filepath.Join(m.backupDir, id)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", os.ErrNotExist
	}
	return path, nil
}

// Copy writes a backup to a response without loading it into memory.
func Copy(dst io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(dst, f)
	return err
}
