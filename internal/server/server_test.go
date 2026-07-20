package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestClientQRAndSubscriptionPayloadRoutes(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	instancePayload := map[string]any{"name": "node", "provider": "jitsi", "transport": "datachannel", "room_id": "https://meet.example/room", "dns": "8.8.8.8:53"}
	resp := p.request(t, http.MethodPost, "/api/v1/instances", instancePayload, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create instance status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodGet, "/api/v1/instances/1/uri?format=client", nil, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("client URI status=%d cache=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	var uriPayload struct {
		URI string `json:"uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uriPayload); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.HasPrefix(uriPayload.URI, "olcrtc://jitsi@r/") || !strings.Contains(uriPayload.URI, "&c=") {
		t.Fatalf("client URI=%q", uriPayload.URI)
	}
	resp = p.request(t, http.MethodGet, "/api/v1/instances/1/qr?format=client", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("client QR status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	slug := "abcdefghijklmnop"
	resp = p.request(t, http.MethodPost, "/api/v1/subscriptions", map[string]any{"slug": slug, "name": "Client", "refresh": "10m", "enabled": true}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodPost, "/api/v1/subscriptions/"+slug+"/entries", map[string]any{"source_instance_id": 1, "enabled": true}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create entry status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodGet, "/api/v1/subscriptions/"+slug+"/payload", nil, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("payload status=%d cache=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	var bundlePayload struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&bundlePayload); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(bundlePayload.Payload, `"type":"olcrtc-sub"`) || !strings.Contains(bundlePayload.Payload, `"uc":false`) {
		t.Fatalf("bundle=%s", bundlePayload.Payload)
	}
	resp = p.request(t, http.MethodGet, "/sub/"+slug+"/removed-projection", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("removed public projection status=%d", resp.StatusCode)
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
	if !strings.Contains(body.String(), "#name: Public") || !strings.Contains(body.String(), "olcrtc://jitsi@r/") || !strings.Contains(body.String(), "&c=") {
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

func TestPublicSubscriptionOpenRedirectsToClient(t *testing.T) {
	p := newTestPanel(t)
	_, err := p.store.CreateSubscription(context.Background(), model.Subscription{Slug: "abcdefghijklmnop", Name: "Public", RefreshInterval: "10m", Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: p.client.Transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(p.server.URL + "/sub/abcdefghijklmnop/open")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "olcrtc://subscription?") || !strings.Contains(location, "url=") || !strings.Contains(location, "name=Public") {
		t.Fatalf("location = %q", location)
	}
}

func TestWriteQRKeepsLongPayloadWhole(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/qr", nil)
	payload := strings.Repeat("token_", 400)
	writeQR(recorder, request, payload, "long.png")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "image/png" || recorder.Body.Len() == 0 {
		t.Fatalf("long QR failed: status=%d type=%q body=%d", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.Len())
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

func TestWaitForTCPStable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := waitForTCPStable(context.Background(), listener.Addr().String(), time.Second, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForTCPStableTimesOut(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if err := waitForTCPStable(context.Background(), address, 100*time.Millisecond, 50*time.Millisecond); err == nil {
		t.Fatal("expected readiness timeout")
	}
}
