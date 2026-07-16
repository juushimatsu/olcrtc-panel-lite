// Command olcrtc-panel runs the HTTPS panel and its administrative CLI.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base32"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/assets"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/certificates"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/config"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/instance"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/security"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/server"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/store"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/subscription"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/systemd"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/traffic"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "olcrtc-panel:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	switch command {
	case "serve":
		return serve(args)
	case "credentials":
		return credentials(args)
	case "certificate":
		return certificate(args)
	case "assets":
		return installAssets(args)
	case "version", "--version", "-version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func installAssets(args []string) error {
	if len(args) == 0 || args[0] != "install" {
		return errors.New("assets action must be install")
	}
	flags := flag.NewFlagSet("assets install", flag.ContinueOnError)
	root := flags.String("root", "/", "filesystem root")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	return assets.Install(*root)
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/olcrtc-panel/config.yaml", "panel YAML config")
	dev := flags.Bool("dev", false, "use a self-contained development directory")
	devDir := flags.String("dev-dir", ".olcrtc-panel-dev", "development data directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath, *dev, *devDir)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, secrets, err := openState(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	count, err := st.AdminCount(context.Background())
	if err != nil {
		return err
	}
	if count == 0 {
		if !*dev {
			return errors.New("administrator is not initialized; run 'olcrtc-panel credentials reset --config ...'")
		}
		username, password, err := resetCredentials(context.Background(), st)
		if err != nil {
			return err
		}
		fmt.Printf("DEV credentials: %s %s\n", username, password)
	}
	if configuredLimit, err := st.SettingOrDefault(context.Background(), "max_instances", ""); err == nil && configuredLimit != "" {
		if value, parseErr := strconv.Atoi(configuredLimit); parseErr == nil && value >= 1 && value <= 1000 {
			cfg.MaxInstances = value
		}
	}
	if publicIP, err := st.SettingOrDefault(context.Background(), "public_ip", ""); err == nil && publicIP != "" {
		cfg.PublicIP = publicIP
	}
	if publicPort, err := st.SettingOrDefault(context.Background(), "public_port", ""); err == nil && publicPort != "" {
		if value, parseErr := strconv.Atoi(publicPort); parseErr == nil && value >= 1 && value <= 65535 {
			cfg.PublicPort = value
			host, _, splitErr := net.SplitHostPort(cfg.Listen)
			if splitErr == nil {
				cfg.Listen = net.JoinHostPort(host, strconv.Itoa(value))
			}
		}
	}
	certInfo, err := certificates.Ensure(cfg.TLSDir, cfg.PublicIP)
	if err != nil {
		return err
	}
	controller := systemd.New(cfg.SystemdEnabled)
	instances := instance.NewManager(st, secrets, controller, cfg.InstancesDir, cfg.RuntimeDir, cfg.MaxInstances)
	baseURL := publicURL(cfg)
	subscriptions := subscription.NewService(st, instances, secrets, baseURL)
	handler := server.New(cfg, st, instances, subscriptions, secrets, logger)
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig: &tls.Config{
			MinVersion:       tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
			CipherSuites:     []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.SystemdEnabled && runtime.GOOS == "linux" {
		collector := traffic.NewCollector(st)
		go supervise(ctx, logger, "traffic collector", collector.Run)
	}
	go resetScheduler(ctx, logger, st)
	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("panel listening", "address", cfg.Listen, "public_url", baseURL, "ca_fingerprint", certInfo.CAFingerprint, "version", cfg.PanelVersion)
		serveErrors <- httpServer.ListenAndServeTLS(certInfo.ServerCertPath, certInfo.ServerKeyPath)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func credentials(args []string) error {
	if len(args) == 0 {
		return errors.New("credentials action is required: reset or set")
	}
	action, args := args[0], args[1:]
	flags := flag.NewFlagSet("credentials "+action, flag.ContinueOnError)
	configPath := flags.String("config", "/etc/olcrtc-panel/config.yaml", "panel YAML config")
	usernameFlag := flags.String("username", "", "new username")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	st, _, err := openState(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	switch action {
	case "reset":
		username, password, err := resetCredentials(context.Background(), st)
		if err != nil {
			return err
		}
		fmt.Printf("username=%s\npassword=%s\n", username, password)
		return nil
	case "set":
		username := strings.TrimSpace(*usernameFlag)
		if len(username) < 3 || len(username) > 64 || strings.ContainsAny(username, " \t\r\n") {
			return errors.New("--username must contain 3-64 non-space characters")
		}
		admin, err := st.Admin(context.Background())
		if err != nil {
			return err
		}
		if err := st.UpdateAdminCredentials(context.Background(), username, admin.PasswordHash); err != nil {
			return err
		}
		fmt.Printf("username=%s\n", username)
		return nil
	default:
		return errors.New("credentials action must be reset or set")
	}
}

func certificate(args []string) error {
	if len(args) == 0 || (args[0] != "regenerate" && args[0] != "ensure") {
		return errors.New("certificate action must be ensure or regenerate")
	}
	action := args[0]
	flags := flag.NewFlagSet("certificate "+action, flag.ContinueOnError)
	configPath := flags.String("config", "/etc/olcrtc-panel/config.yaml", "panel YAML config")
	publicIP := flags.String("public-ip", "", "new public IP")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *publicIP != "" {
		cfg.PublicIP = *publicIP
	}
	var info certificates.Info
	if action == "ensure" {
		info, err = certificates.Ensure(cfg.TLSDir, cfg.PublicIP)
	} else {
		info, err = certificates.RegenerateServer(cfg.TLSDir, cfg.PublicIP)
	}
	if err != nil {
		return err
	}
	fmt.Printf("ca_fingerprint=%s\nserver_fingerprint=%s\n", info.CAFingerprint, info.ServerFingerprint)
	return nil
}

func loadConfig(path string, dev bool, devDir string) (config.Config, error) {
	if !dev {
		return config.Load(path)
	}
	abs, err := filepath.Abs(devDir)
	if err != nil {
		return config.Config{}, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return config.Config{}, err
	}
	return config.Dev(abs), nil
}

func openState(cfg config.Config) (*store.Store, *security.Secrets, error) {
	key, err := security.LoadOrCreateMasterKey(cfg.MasterKeyPath)
	if err != nil {
		return nil, nil, err
	}
	secrets, err := security.NewSecrets(key)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return nil, nil, err
	}
	return st, secrets, nil
}

func resetCredentials(ctx context.Context, st *store.Store) (string, string, error) {
	randomName := make([]byte, 7)
	if _, err := rand.Read(randomName); err != nil {
		return "", "", err
	}
	username := "admin_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randomName))[:10]
	password, err := security.RandomToken(32)
	if err != nil {
		return "", "", err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return "", "", err
	}
	count, err := st.AdminCount(ctx)
	if err != nil {
		return "", "", err
	}
	if count == 0 {
		err = st.CreateAdmin(ctx, username, hash)
	} else {
		err = st.UpdateAdminCredentials(ctx, username, hash)
	}
	return username, password, err
}

func publicURL(cfg config.Config) string {
	host := cfg.PublicIP
	if host == "" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host + ":" + strconv.Itoa(cfg.PublicPort)
}

func supervise(ctx context.Context, logger *slog.Logger, name string, run func(context.Context) error) {
	for ctx.Err() == nil {
		if err := run(ctx); err != nil && ctx.Err() == nil {
			logger.Error(name+" stopped", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func resetScheduler(ctx context.Context, logger *slog.Logger, st *store.Store) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			items, err := st.Instances(ctx)
			if err != nil {
				logger.Error("traffic reset scan", "error", err)
				continue
			}
			for _, item := range items {
				counter, err := st.TrafficCounter(ctx, item.ID)
				if err == nil && traffic.ResetDue(item.ResetPolicy, counter.PeriodStartedAt, now) {
					if err := st.ResetTraffic(ctx, item.ID, now); err != nil {
						logger.Error("traffic reset", "instance_id", item.ID, "error", err)
					}
				}
			}
		}
	}
}
