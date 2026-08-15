package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
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
	return newTestPanelWithConfig(t, nil)
}

func newTestPanelWithConfig(t *testing.T, configure func(*config.Config)) testPanel {
	t.Helper()
	root := t.TempDir()
	cfg := config.Dev(root)
	if configure != nil {
		configure(&cfg)
	}
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
	instances := instance.NewManager(st, secrets, systemd.New(false), cfg.InstancesDir, cfg.RuntimeDir, cfg.ReleaseDir, 20)
	subscriptions := subscription.NewServiceAtSubscriptionPath(st, instances, secrets, cfg.PublicSubscriptionBaseURL())
	handler := New(cfg, st, instances, subscriptions, secrets, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	client := ts.Client()
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	return testPanel{server: ts, client: client, store: st}
}

func loginTestPanelAt(t *testing.T, p testPanel, panelPath string) string {
	t.Helper()
	resp := p.request(t, http.MethodPost, config.JoinURLPath(panelPath, "/api/v1/auth/login"), map[string]string{"username": "admin_test", "password": "test-password-12345"}, "")
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

func TestCustomPanelAndSubscriptionMounts(t *testing.T) {
	const panelPath = "/control-a8f3"
	const subscriptionPath = "/feeds-b19c"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PublicOrigin = "https://panel.example"
		cfg.PanelPath = panelPath
		cfg.SubscriptionPath = subscriptionPath
	})

	noRedirect := *p.client
	noRedirect.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(http.MethodGet, p.server.URL+panelPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != panelPath+"/" {
		t.Fatalf("canonical redirect status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	for path, want := range map[string]int{
		"/":                                 http.StatusNotFound,
		"/api/v1/system/status":             http.StatusNotFound,
		panelPath + "/api/v1/system/status": http.StatusUnauthorized,
	} {
		resp = p.request(t, http.MethodGet, path, nil, "")
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("GET %s status=%d want=%d", path, resp.StatusCode, want)
		}
	}

	csrf := loginTestPanelAt(t, p, panelPath)
	resp = p.request(t, http.MethodPost, panelPath+"/api/v1/subscriptions", map[string]any{"name": "custom", "slug": "abcdefghijklmnop", "refresh": "10m", "enabled": true}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodGet, subscriptionPath+"/abcdefghijklmnop", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("custom subscription status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodGet, "/sub/abcdefghijklmnop", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy subscription route status=%d", resp.StatusCode)
	}
}

func TestCustomPanelCookiesAndTrustedProxy(t *testing.T) {
	const panelPath = "/private-panel"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) { cfg.PanelPath = panelPath })
	resp := p.request(t, http.MethodPost, panelPath+"/api/v1/auth/login", map[string]string{"username": "admin_test", "password": "test-password-12345"}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d", resp.StatusCode)
	}
	paths := map[string]int{}
	for _, cookie := range resp.Cookies() {
		paths[cookie.Path]++
	}
	if paths[panelPath] != 2 || paths["/"] != 2 {
		t.Fatalf("unexpected cookie paths: %#v", paths)
	}

	server := &Server{trustedProxies: parseTrustedProxies([]string{"127.0.0.1/32", "10.0.0.0/8"})}
	untrusted := httptest.NewRequest(http.MethodGet, "https://panel.example/", nil)
	untrusted.RemoteAddr = "203.0.113.10:1234"
	untrusted.Header.Set("X-Forwarded-For", "198.51.100.20")
	if got := server.resolveClientIP(untrusted); got != "203.0.113.10" {
		t.Fatalf("untrusted XFF resolved to %q", got)
	}
	trusted := httptest.NewRequest(http.MethodGet, "https://panel.example/", nil)
	trusted.RemoteAddr = "127.0.0.1:1234"
	trusted.Header.Set("X-Forwarded-For", "198.51.100.20, 10.0.0.2")
	if got := server.resolveClientIP(trusted); got != "198.51.100.20" {
		t.Fatalf("trusted XFF resolved to %q", got)
	}
}

func TestPublicURLSettingsAreValidatedAndSavedTogether(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	if err := p.store.SetSettings(context.Background(), map[string]string{"public_origin": "https://old.example", "panel_path": "/old-panel", "subscription_path": "/old-feeds"}); err != nil {
		t.Fatal(err)
	}
	resp := p.request(t, http.MethodPut, "/api/v1/settings", map[string]any{"public_origin": "https://new.example", "panel_path": "/same", "subscription_path": "/same/feeds"}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid settings status=%d", resp.StatusCode)
	}
	for key, want := range map[string]string{"public_origin": "https://old.example", "panel_path": "/old-panel", "subscription_path": "/old-feeds"} {
		got, _, err := p.store.Setting(context.Background(), key)
		if err != nil || got != want {
			t.Fatalf("setting %s=%q, %v want=%q", key, got, err, want)
		}
	}

	resp = p.request(t, http.MethodPut, "/api/v1/settings", map[string]any{"public_origin": "https://new.example", "panel_path": "/new-panel", "subscription_path": "/new-feeds"}, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid settings status=%d", resp.StatusCode)
	}
	var payload struct {
		HTTPS struct {
			PanelURL        string `json:"panel_url"`
			SubscriptionURL string `json:"subscription_url"`
			RestartRequired bool   `json:"restart_required"`
		} `json:"https"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.HTTPS.PanelURL != "https://new.example/new-panel" || payload.HTTPS.SubscriptionURL != "https://new.example/new-feeds" || !payload.HTTPS.RestartRequired {
		t.Fatalf("unexpected public settings response: %#v", payload.HTTPS)
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

func TestLinkedSubscriptionUsesUpdatedInstanceData(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	slug := "updatedinstancefeed"

	resp := p.request(t, http.MethodPost, "/api/v1/instances", map[string]any{
		"name": "old-node", "provider": "jitsi", "transport": "datachannel",
		"room_id": "https://meet.example/old-room", "dns": "8.8.8.8:53",
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create instance status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodPost, "/api/v1/subscriptions", map[string]any{
		"slug": slug, "name": "Updated instance", "refresh": "10m", "enabled": true,
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodPost, "/api/v1/subscriptions/"+slug+"/entries", map[string]any{
		"source_instance_id": 1, "enabled": true,
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create entry status=%d", resp.StatusCode)
	}
	readFeed := func() string {
		response := p.request(t, http.MethodGet, "/sub/"+slug, nil, "")
		defer response.Body.Close()
		var body bytes.Buffer
		_, _ = body.ReadFrom(response.Body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("subscription status=%d body=%s", response.StatusCode, body.String())
		}
		return body.String()
	}
	feedRevision := func(feed string) string {
		for _, line := range strings.Split(feed, "\n") {
			if strings.HasPrefix(line, "#update: ") {
				return strings.TrimPrefix(line, "#update: ")
			}
		}
		t.Fatalf("subscription feed has no #update:\n%s", feed)
		return ""
	}
	oldRevision := feedRevision(readFeed())

	resp = p.request(t, http.MethodPut, "/api/v1/instances/1", map[string]any{
		"name": "new-node", "provider": "jitsi", "transport": "datachannel",
		"room_id": "https://meet.example/new-room", "dns": "1.1.1.1:53",
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update instance status=%d", resp.StatusCode)
	}

	feed := readFeed()
	if revision := feedRevision(feed); revision == oldRevision {
		t.Fatalf("subscription revision did not change after linked instance update: %s", revision)
	}
	for _, want := range []string{"https%3A%2F%2Fmeet.example%2Fnew-room", "d=1.1.1.1%3A53", "#new-node"} {
		if !strings.Contains(feed, want) {
			t.Fatalf("updated value %q missing from feed:\n%s", want, feed)
		}
	}
	for _, stale := range []string{"old-room", "8.8.8.8", "old-node"} {
		if strings.Contains(feed, stale) {
			t.Fatalf("stale value %q remains in feed:\n%s", stale, feed)
		}
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

func TestOLCBOXSubscriptionProjectionAndQR(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	resp := p.request(t, http.MethodPost, "/api/v1/instances", map[string]any{
		"name": "olcbox-node", "provider": "jitsi", "transport": "datachannel",
		"room_id": "https://meet.example/room", "dns": "8.8.8.8:53",
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create instance status=%d", resp.StatusCode)
	}
	slug := "olcboxabcdefghijkl"
	resp = p.request(t, http.MethodPost, "/api/v1/subscriptions", map[string]any{
		"slug": slug, "name": "OLCBOX", "refresh": "10m", "enabled": true,
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodPost, "/api/v1/subscriptions/"+slug+"/entries", map[string]any{
		"source_instance_id": 1, "enabled": true,
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create entry status=%d", resp.StatusCode)
	}
	manual := "olcrtc://jitsi?datachannel@https://meet.example/manual#" + strings.Repeat("d", 64) + "$Manual OLCBOX"
	resp = p.request(t, http.MethodPost, "/api/v1/subscriptions/"+slug+"/entries", map[string]any{
		"raw_uri": manual, "name": "Manual OLCBOX", "enabled": true,
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create OLCBOX manual entry status=%d", resp.StatusCode)
	}

	resp = p.request(t, http.MethodGet, "/api/v1/subscriptions/"+slug+"/payload?format=olcbox", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OLCBOX payload status=%d", resp.StatusCode)
	}
	var payload struct {
		Format  string `json:"format"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if payload.Format != "olcbox" || !strings.HasSuffix(payload.Payload, "/sub/"+slug+"/olcbox") {
		t.Fatalf("unexpected OLCBOX payload: %#v", payload)
	}

	resp = p.request(t, http.MethodGet, "/api/v1/subscriptions/"+slug+"/qr?format=olcbox", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("OLCBOX QR status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	anonymous := &http.Client{Transport: p.client.Transport}
	resp, err := anonymous.Get(p.server.URL + "/sub/" + slug + "/olcbox")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" || resp.Header.Get("Profile-Update-Interval") != "1" || !strings.Contains(body.String(), "olcrtc://jitsi?datachannel@") || !strings.Contains(body.String(), manual) || strings.Contains(body.String(), "@r/") {
		t.Fatalf("unexpected OLCBOX feed status=%d type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), body.String())
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

func TestAutomationCanonicalAndLegacyRoutes(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)

	for _, path := range []string{"/api/v1/wb/components", "/api/v1/automation/components"} {
		resp := p.request(t, http.MethodGet, path, nil, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, resp.StatusCode)
		}
	}

	resp := p.request(t, http.MethodPost, "/api/v1/automation/telemost/session", map[string]string{"action": "refresh"}, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Telemost refresh status=%d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "invalid_action" {
		t.Fatalf("Telemost refresh code=%q", payload.Error.Code)
	}
}

func TestAutomationProxySettingsSupportAuthentication(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	resp := p.request(t, http.MethodPut, "/api/v1/automation/settings", map[string]any{
		"proxy_mode":     "socks5",
		"proxy_address":  "proxy.example:1080",
		"proxy_username": "proxy-user",
		"proxy_password": "proxy-secret",
	}, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy settings status=%d", resp.StatusCode)
	}
	var saved struct {
		Mode        string `json:"proxy_mode"`
		Address     string `json:"proxy_address"`
		Username    string `json:"proxy_username"`
		PasswordSet bool   `json:"proxy_password_set"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Mode != "socks5" || saved.Address != "proxy.example:1080" || saved.Username != "proxy-user" || !saved.PasswordSet {
		t.Fatalf("unexpected proxy response: %#v", saved)
	}
	ciphertext, encrypted, err := p.store.Setting(context.Background(), "wb_proxy_password")
	if err != nil || !encrypted || ciphertext == "proxy-secret" {
		t.Fatalf("proxy password was not stored encrypted: encrypted=%v value=%q err=%v", encrypted, ciphertext, err)
	}

	invalid := p.request(t, http.MethodPut, "/api/v1/automation/settings", map[string]any{
		"proxy_mode":    "http",
		"proxy_address": "http://proxy.example:8080",
	}, csrf)
	defer invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid proxy status=%d", invalid.StatusCode)
	}
}

func TestAutomationProviderProfilesAreIsolated(t *testing.T) {
	if wb, telemost := automationProfileDir(automationWBProvider), automationProfileDir(automationTeleProvider); wb == telemost {
		t.Fatalf("provider profiles share path %q", wb)
	}

	root := t.TempDir()
	wbProfile := filepath.Join(root, automationWBProvider)
	telemostProfile := filepath.Join(root, automationTeleProvider)
	for _, path := range []string{wbProfile, telemostProfile} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(wbProfile, "cookie.sqlite")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeAutomationProfile(root, automationTeleProvider); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("reset Telemost removed WB profile: %v", err)
	}
	if _, err := os.Stat(telemostProfile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Telemost profile still exists: %v", err)
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
