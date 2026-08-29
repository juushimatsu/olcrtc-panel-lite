package server

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
)

func TestPanelPathTrailingSlashRedirect(t *testing.T) {
	const panelPath = "/my-panel"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PanelPath = panelPath
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("status=%d want=308", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != panelPath+"/" {
		t.Fatalf("Location=%q want=%q", loc, panelPath+"/")
	}
}

func TestRootPanelPathNoRedirect(t *testing.T) {
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PanelPath = "/"
	})
	noRedirect := *p.client
	noRedirect.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := noRedirect.Get(p.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPermanentRedirect {
		t.Fatal("root panel path should not redirect")
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "olcRTC Panel Lite") {
		t.Fatalf("root should serve SPA HTML, got: %s", string(body)[:min(200, len(body))])
	}
}

func TestCustomPanelPathBlocksRootAPIAccess(t *testing.T) {
	const panelPath = "/secure-admin"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PanelPath = panelPath
	})

	for _, path := range []string{
		"/api/v1/auth/me",
		"/api/v1/system/status",
		"/api/v1/instances",
	} {
		resp := p.request(t, http.MethodGet, path, nil, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status=%d want=404 (should be blocked at root)", path, resp.StatusCode)
		}
	}
}

func TestCustomPanelFrontendServesHTMLWithBaseInjected(t *testing.T) {
	const panelPath = "/injected-base"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PanelPath = panelPath
	})

	resp := p.request(t, http.MethodGet, panelPath+"/", nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, `content="`+panelPath+`"`) {
		t.Fatalf("panel base not injected into HTML: %s", html[:min(500, len(html))])
	}
	if strings.Contains(html, "__OLCRTC_PANEL_BASE__") {
		t.Fatal("placeholder __OLCRTC_PANEL_BASE__ was not replaced")
	}
	if strings.Contains(html, "__OLCRTC_SUBSCRIPTION_BASE__") {
		t.Fatal("placeholder __OLCRTC_SUBSCRIPTION_BASE__ was not replaced")
	}
}

func TestFrontendFallbackServesSPAForNonDotPaths(t *testing.T) {
	const panelPath = "/spa-test"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PanelPath = panelPath
	})

	for _, subpath := range []string{"/", "/some/deep/path", "/dashboard", "/instances"} {
		resp := p.request(t, http.MethodGet, panelPath+subpath, nil, "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s%s status=%d want=200", panelPath, subpath, resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			t.Fatalf("GET %s%s content-type=%q want text/html", panelPath, subpath, ct)
		}
	}
}

func TestStaticAssetsNotAffectedBySPAFallback(t *testing.T) {
	const panelPath = "/static-test"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PanelPath = panelPath
	})

	resp := p.request(t, http.MethodGet, panelPath+"/app.js", nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("app.js status=%d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		t.Fatal("app.js should not be served as HTML")
	}
	cacheControl := resp.Header.Get("Cache-Control")
	if !strings.Contains(cacheControl, "max-age") {
		t.Fatalf("static asset should have cache max-age, got: %q", cacheControl)
	}
}

func TestSubscriptionCatchAllReturns404(t *testing.T) {
	p := newTestPanel(t)

	for _, path := range []string{
		"/sub/someslug/unknown-subpath",
		"/sub/someslug/deep/nested/path",
	} {
		resp := p.request(t, http.MethodGet, path, nil, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status=%d want=404", path, resp.StatusCode)
		}
	}
}

func TestSecurityHeadersOnCustomPanelPath(t *testing.T) {
	const panelPath = "/hdr-test"
	p := newTestPanelWithConfig(t, func(cfg *config.Config) {
		cfg.PanelPath = panelPath
	})

	resp := p.request(t, http.MethodGet, panelPath+"/", nil, "")
	defer resp.Body.Close()

	for _, header := range []string{"X-Content-Type-Options", "X-Frame-Options", "Content-Security-Policy", "Referrer-Policy"} {
		if v := resp.Header.Get(header); v == "" {
			t.Fatalf("missing security header %q on custom panel path", header)
		}
	}
}

func TestJoinURLPathEdgeCases(t *testing.T) {
	cases := []struct {
		base, route, want string
	}{
		{"/", "/api/v1/test", "/api/v1/test"},
		{"/panel", "/api/v1/test", "/panel/api/v1/test"},
		{"/panel", "/", "/panel/"},
		{"/panel", "", "/panel/"},
		{"/a/b", "/c/d", "/a/b/c/d"},
		{"/", "/", "/"},
	}
	for _, tc := range cases {
		got := config.JoinURLPath(tc.base, tc.route)
		if got != tc.want {
			t.Errorf("JoinURLPath(%q, %q) = %q, want %q", tc.base, tc.route, got, tc.want)
		}
	}
}

func TestValidateMountPathRejectsDangerousInputs(t *testing.T) {
	dangerous := []string{
		"",
		"no-leading-slash",
		"/trailing/",
		"//double",
		"/has space",
		"/has?query",
		"/has#fragment",
		"/has%2fencoded",
		"/has\\backslash",
		"/../traversal",
		"/./dot",
	}
	for _, v := range dangerous {
		if err := config.ValidateMountPath(v, false); err == nil {
			t.Errorf("ValidateMountPath(%q, false) should reject", v)
		}
	}

	valid := []string{"/sub", "/panel", "/a/b/c", "/my-panel.v2"}
	for _, v := range valid {
		if err := config.ValidateMountPath(v, false); err != nil {
			t.Errorf("ValidateMountPath(%q, false) should accept: %v", v, err)
		}
	}

	if err := config.ValidateMountPath("/", true); err != nil {
		t.Errorf("ValidateMountPath(\"/\", true) should accept root: %v", err)
	}
	if err := config.ValidateMountPath("/", false); err == nil {
		t.Error("ValidateMountPath(\"/\", false) should reject root")
	}
}
