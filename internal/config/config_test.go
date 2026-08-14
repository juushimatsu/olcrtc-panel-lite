package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLegacyConfigUsesPublicURLDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:8443\ndatabase_path: panel.db\nmaster_key_path: master.key\ninstances_dir: instances\ntls_dir: tls\nmax_instances: 20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelPath != "/" || cfg.SubscriptionPath != "/sub" || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("legacy defaults not preserved: %#v", cfg)
	}
}

func TestValidatePublicURLSettings(t *testing.T) {
	cfg := Default()
	cfg.PublicOrigin = "https://example.com:9443"
	cfg.PanelPath = "/control-a8f3"
	cfg.SubscriptionPath = "/feeds-b19c"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid custom URL settings rejected: %v", err)
	}
	invalid := []Config{}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.PublicOrigin = "http://example.com" },
		func(c *Config) { c.PublicOrigin = "https://user@example.com" },
		func(c *Config) { c.PublicOrigin = "https://example.com/" },
		func(c *Config) { c.PanelPath = "/control/../admin" },
		func(c *Config) { c.PanelPath = "/control%2fadmin" },
		func(c *Config) { c.PanelPath = "/control/" },
		func(c *Config) { c.SubscriptionPath = "/" },
		func(c *Config) { c.PanelPath = "/control"; c.SubscriptionPath = "/control/feeds" },
	} {
		item := cfg
		mutate(&item)
		invalid = append(invalid, item)
	}
	for _, item := range invalid {
		if err := item.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %#v", item)
		}
	}
}

func TestPublicURLsSupportIPv4AndIPv6(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want string
	}{
		{ip: "203.0.113.10", want: "https://203.0.113.10:8443"},
		{ip: "2001:db8::10", want: "https://[2001:db8::10]:8443"},
	} {
		cfg := Default()
		cfg.PublicIP = tc.ip
		if got := cfg.PublicBaseURL(); got != tc.want {
			t.Fatalf("PublicBaseURL(%q)=%q want=%q", tc.ip, got, tc.want)
		}
	}
}
