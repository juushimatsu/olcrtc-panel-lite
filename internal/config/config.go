// Package config loads panel runtime configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config controls filesystem paths, HTTPS and external services.
type Config struct {
	Listen             string `yaml:"listen"`
	PublicIP           string `yaml:"public_ip"`
	PublicPort         int    `yaml:"public_port"`
	DatabasePath       string `yaml:"database_path"`
	MasterKeyPath      string `yaml:"master_key_path"`
	InstancesDir       string `yaml:"instances_dir"`
	RuntimeDir         string `yaml:"runtime_dir"`
	TLSDir             string `yaml:"tls_dir"`
	BackupDir          string `yaml:"backup_dir"`
	ReleaseDir         string `yaml:"release_dir"`
	OlcrtcBinary       string `yaml:"olcrtc_binary"`
	SystemdEnabled     bool   `yaml:"systemd_enabled"`
	MaxInstances       int    `yaml:"max_instances"`
	CookieName         string `yaml:"cookie_name"`
	HSTS               bool   `yaml:"hsts"`
	ReleaseManifestURL string `yaml:"release_manifest_url"`
	UpstreamSHA        string `yaml:"upstream_sha"`
	PanelVersion       string `yaml:"panel_version"`
}

// Default returns production filesystem defaults.
func Default() Config {
	return Config{
		Listen:         "0.0.0.0:8443",
		PublicPort:     8443,
		DatabasePath:   "/var/lib/olcrtc-panel/panel.db",
		MasterKeyPath:  "/etc/olcrtc-panel/master.key",
		InstancesDir:   "/etc/olcrtc-panel/instances",
		RuntimeDir:     "/var/lib/olcrtc",
		TLSDir:         "/var/lib/olcrtc-panel/tls",
		BackupDir:      "/var/lib/olcrtc-panel/backups",
		ReleaseDir:     "/var/lib/olcrtc-panel/releases",
		OlcrtcBinary:   "/usr/local/bin/olcrtc",
		SystemdEnabled: true,
		MaxInstances:   20,
		CookieName:     "olcrtc_panel_session",
		PanelVersion:   "dev",
	}
}

// Load parses a YAML file and fills missing values from Default.
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects unsafe or incomplete runtime settings.
func (c Config) Validate() error {
	if c.Listen == "" || c.DatabasePath == "" || c.MasterKeyPath == "" || c.InstancesDir == "" || c.TLSDir == "" {
		return errors.New("required panel path or listen setting is empty")
	}
	if c.PublicPort < 1 || c.PublicPort > 65535 {
		return errors.New("public_port must be in range 1..65535")
	}
	if c.MaxInstances < 1 || c.MaxInstances > 1000 {
		return errors.New("max_instances must be in range 1..1000")
	}
	return nil
}

// Dev returns a self-contained local configuration for development and tests.
func Dev(root string) Config {
	cfg := Default()
	cfg.Listen = "127.0.0.1:8443"
	cfg.PublicIP = "127.0.0.1"
	cfg.DatabasePath = filepath.Join(root, "panel.db")
	cfg.MasterKeyPath = filepath.Join(root, "master.key")
	cfg.InstancesDir = filepath.Join(root, "instances")
	cfg.RuntimeDir = filepath.Join(root, "runtime")
	cfg.TLSDir = filepath.Join(root, "tls")
	cfg.BackupDir = filepath.Join(root, "backups")
	cfg.ReleaseDir = filepath.Join(root, "releases")
	cfg.SystemdEnabled = false
	cfg.PanelVersion = "dev"
	return cfg
}
