// Package config loads panel runtime configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config controls filesystem paths, HTTPS and external services.
type Config struct {
	Listen             string   `yaml:"listen"`
	PublicIP           string   `yaml:"public_ip"`
	PublicPort         int      `yaml:"public_port"`
	PublicOrigin       string   `yaml:"public_origin"`
	PanelPath          string   `yaml:"panel_path"`
	SubscriptionPath   string   `yaml:"subscription_path"`
	TrustedProxies     []string `yaml:"trusted_proxies"`
	DatabasePath       string   `yaml:"database_path"`
	MasterKeyPath      string   `yaml:"master_key_path"`
	InstancesDir       string   `yaml:"instances_dir"`
	RuntimeDir         string   `yaml:"runtime_dir"`
	TLSDir             string   `yaml:"tls_dir"`
	BackupDir          string   `yaml:"backup_dir"`
	ReleaseDir         string   `yaml:"release_dir"`
	OlcrtcBinary       string   `yaml:"olcrtc_binary"`
	SystemdEnabled     bool     `yaml:"systemd_enabled"`
	MaxInstances       int      `yaml:"max_instances"`
	CookieName         string   `yaml:"cookie_name"`
	HSTS               bool     `yaml:"hsts"`
	ReleaseManifestURL string   `yaml:"release_manifest_url"`
	UpstreamSHA        string   `yaml:"upstream_sha"`
	PanelVersion       string   `yaml:"panel_version"`
}

// Default returns production filesystem defaults.
func Default() Config {
	return Config{
		Listen:           "0.0.0.0:8443",
		PublicPort:       8443,
		PanelPath:        "/",
		SubscriptionPath: "/sub",
		TrustedProxies:   []string{"127.0.0.1/32", "::1/128"},
		DatabasePath:     "/var/lib/olcrtc-panel/panel.db",
		MasterKeyPath:    "/etc/olcrtc-panel/master.key",
		InstancesDir:     "/etc/olcrtc-panel/instances",
		RuntimeDir:       "/var/lib/olcrtc",
		TLSDir:           "/var/lib/olcrtc-panel/tls",
		BackupDir:        "/var/lib/olcrtc-panel/backups",
		ReleaseDir:       "/var/lib/olcrtc-panel/releases",
		OlcrtcBinary:     "/usr/local/bin/olcrtc",
		SystemdEnabled:   true,
		MaxInstances:     20,
		CookieName:       "olcrtc_panel_session",
		PanelVersion:     "dev",
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
	if err := ValidatePublicOrigin(c.PublicOrigin); err != nil {
		return err
	}
	if err := ValidateMountPath(c.PanelPath, true); err != nil {
		return fmt.Errorf("panel_path: %w", err)
	}
	if err := ValidateMountPath(c.SubscriptionPath, false); err != nil {
		return fmt.Errorf("subscription_path: %w", err)
	}
	if c.PanelPath != "/" && pathsOverlap(c.PanelPath, c.SubscriptionPath) {
		return errors.New("panel_path and subscription_path must not overlap")
	}
	if err := ValidateTrustedProxies(c.TrustedProxies); err != nil {
		return err
	}
	return nil
}

var mountSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// ValidatePublicOrigin accepts an HTTPS origin without credentials, path,
// query, or fragment. An empty value keeps the legacy public_ip/public_port URL.
func ValidatePublicOrigin(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("public_origin must be an absolute HTTPS origin")
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("public_origin must not contain credentials, path, query, or fragment")
	}
	if parsed.Hostname() == "" {
		return errors.New("public_origin must contain a host")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("public_origin port must be in range 1..65535")
		}
	}
	return nil
}

// ValidateMountPath accepts canonical, unescaped URL path segments only.
func ValidateMountPath(value string, allowRoot bool) error {
	if value == "" || !strings.HasPrefix(value, "/") {
		return errors.New("must start with '/'")
	}
	if value == "/" {
		if allowRoot {
			return nil
		}
		return errors.New("must not be root")
	}
	if strings.HasSuffix(value, "/") || strings.Contains(value, "//") || strings.ContainsAny(value, "?#\\%") {
		return errors.New("must be canonical and must not contain trailing slash, query, fragment, backslash, or escapes")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "." || segment == ".." || !mountSegmentPattern.MatchString(segment) {
			return errors.New("contains an unsafe path segment")
		}
	}
	return nil
}

// ValidateTrustedProxies validates the CIDRs allowed to supply X-Forwarded-For.
func ValidateTrustedProxies(values []string) error {
	for _, value := range values {
		if strings.TrimSpace(value) != value || value == "" {
			return errors.New("trusted_proxies entries must be non-empty CIDRs without surrounding whitespace")
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("trusted_proxies contains invalid CIDR %q", value)
		}
	}
	return nil
}

func pathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

// PublicBaseURL returns the configured external origin or the legacy IP URL.
func (c Config) PublicBaseURL() string {
	if origin := strings.TrimSuffix(strings.TrimSpace(c.PublicOrigin), "/"); origin != "" {
		return origin
	}
	host := c.PublicIP
	if host == "" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host + ":" + strconv.Itoa(c.PublicPort)
}

// PublicPanelURL returns the external URL of the panel mount.
func (c Config) PublicPanelURL() string {
	return strings.TrimSuffix(c.PublicBaseURL(), "/") + c.PanelPath
}

// JoinURLPath joins a validated mount path and an absolute route path.
func JoinURLPath(base, route string) string {
	if base == "/" {
		return "/" + strings.TrimPrefix(route, "/")
	}
	if route == "/" || route == "" {
		return base + "/"
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(route, "/")
}

// PublicSubscriptionBaseURL returns the external base URL for subscription slugs.
func (c Config) PublicSubscriptionBaseURL() string {
	return strings.TrimSuffix(c.PublicBaseURL(), "/") + c.SubscriptionPath
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
