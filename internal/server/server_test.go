package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/instance"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/subscription"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/systemd"
)

type testPanel struct {
	server *httptest.Server
	client *http.Client
	store  *store.Store
}

func newTestPanel(t *testing.T) testPanel {
	t.Helper()
	root := t.TempDir()
	cfg := config.Dev(root)
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := security.HashPassword("test-password-12345")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAdmin(context.Background(), "admin_test", hash); err != nil {
		t.Fatal(err)
	}
	secrets, _ := security.NewSecrets(make([]byte, 32))
	instances := instance.NewManager(st, secrets, systemd.New(false), cfg.InstancesDir, cfg.RuntimeDir, 20)
	subscriptions := subscription.NewService(st, instances, secrets, "https://panel.test:8443")
	handler := New(cfg, st, instances, subscriptions, secrets, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	client := ts.Client()
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	return testPanel{server: ts, client: client, store: st}
}

func (p testPanel) request(t *testing.T, method, path string, body any, csrf string) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, p.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func loginTestPanel(t *testing.T, p testPanel) string {
	t.Helper()
	resp := p.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin_test", "password": "test-password-12345"}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d", resp.StatusCode)
	}
	var payload struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.CSRF == "" {
		t.Fatal("empty CSRF")
	}
	return payload.CSRF
}

func TestAuthAndCSRF(t *testing.T) {
	p := newTestPanel(t)
	resp := p.request(t, http.MethodGet, "/api/v1/system/status", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", resp.StatusCode)
	}
	csrf := loginTestPanel(t, p)
	payload := map[string]any{"name": "node", "provider": "jitsi", "transport": "datachannel", "room_id": "https://meet.example/room", "dns": "8.8.8.8:53"}
	resp = p.request(t, http.MethodPost, "/api/v1/instances", payload, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodPost, "/api/v1/instances", payload, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", resp.StatusCode)
	}
}

func TestPublicSubscriptionIsolation(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	payload := map[string]any{"name": "node", "provider": "jitsi", "transport": "datachannel", "room_id": "https://meet.example/room", "dns": "8.8.8.8:53"}
	resp := p.request(t, http.MethodPost, "/api/v1/instances", payload, csrf)
	resp.Body.Close()
	items, err := p.store.Instances(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%v err=%v", items, err)
	}
	sub, err := p.store.CreateSubscription(context.Background(), model.Subscription{Slug: "abcdefghijklmnop", Name: "Public", RefreshInterval: "10m", Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	id := items[0].ID
	_, err = p.store.AddSubscriptionEntry(context.Background(), model.SubscriptionEntry{SubscriptionID: sub.ID, SourceInstanceID: &id, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	anonymous := &http.Client{Transport: p.client.Transport}
	resp, err = anonymous.Get(p.server.URL + "/sub/abcdefghijklmnop")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public status=%d", resp.StatusCode)
	}
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if !strings.Contains(body.String(), "#name: Public") || !strings.Contains(body.String(), "olcrtc://jitsi?") {
		t.Fatalf("body=%s", body.String())
	}
	resp, err = anonymous.Get(p.server.URL + "/api/v1/instances")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin API exposed: %d", resp.StatusCode)
	}
}

func TestLoginLimiterBlocksSixthAttempt(t *testing.T) {
	limiter := newLoginLimiter()
	ip := "203.0.113.1"
	for range 5 {
		limiter.fail(ip)
	}
	allowed, _ := limiter.allow(ip)
	if allowed {
		t.Fatal("sixth login attempt was allowed")
	}
}

func TestJWTExpiration(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":2000000000}`))
	expires, ok := jwtExpiration("x." + payload + ".y")
	if !ok || expires.Unix() != 2000000000 {
		t.Fatalf("expires=%v ok=%v", expires, ok)
	}
}
