package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestWBCreateFlowFillsCapturedTokenField(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, required := range []string{
		"token=current.state?.token||''",
		"form.elements.auth_token.value=token",
		"WB account token заполнены",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("WB create UI is missing %q", required)
		}
	}
}

func TestSubscriptionUIExposesClientAndOLCBOXProjections(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, required := range []string{
		"QR OLCRTC Client",
		"QR OLCBOX",
		"subscriptionURL(sub.slug, '/olcbox')",
		"payload?format=${format}",
		"OLCBOX URI — в OLCBOX feed",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("subscription UI is missing %q", required)
		}
	}
}

func TestFrontendUsesInjectedPanelAndSubscriptionBases(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, required := range []string{
		`meta[name="olcrtc-panel-base"]`,
		`meta[name="olcrtc-subscription-base"]`,
		"fetch(panelURL(path)",
		"panelURL(`/api/v1/instances/${id}/qr?format=${format}`)",
		"panelURL(`/api/v1/subscriptions/${encodeURIComponent(slug)}/qr?format=${format}`)",
		"subscriptionURL(sub.slug, '/open')",
		"function updatePublicURLPreview(form)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("custom path support is missing %q", required)
		}
	}
	if strings.Contains(source, "location.origin+'/sub/'") || strings.Contains(source, "`${location.origin}/sub/") {
		t.Fatal("frontend still builds subscription URLs from the domain root")
	}
}

func TestWBSettingsActionsWrapWithinSection(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := fs.ReadFile(Static, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(app), `class="form-actions wb-actions"`) {
		t.Fatal("WB settings actions are missing their scoped layout class")
	}
	if !strings.Contains(string(styles), `.wb-actions { justify-content: flex-start; flex-wrap: wrap; }`) {
		t.Fatal("WB settings actions do not wrap within their section")
	}
}

func TestTelemostRoomInputIsNormalized(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, required := range []string{
		"function normalizeRoomID(provider, room)",
		"room_id: normalizeRoomID(provider, d.get('room_id'))",
		"app.addEventListener('focusout', normalizeInstanceRoomField)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Telemost Room ID normalization is missing %q", required)
		}
	}
}

func TestTelemostPlaywrightFlowFillsInstanceRoom(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, required := range []string{
		`data-action="telemost-fill-instance"`,
		"api('/api/v1/automation/telemost/session'",
		"waitForAutomationSession('telemost')",
		"form.elements.provider.value='telemost'",
		"form.elements.transport.value='vp8channel'",
		"normalizeRoomID('telemost',current.state?.room_id||'')",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Telemost Playwright UI flow is missing %q", required)
		}
	}
}

func TestInstanceDNSSelectorUsesGoogleDefaultAndPresets(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, required := range []string{
		"dns:'8.8.8.8:53'",
		"Google (8.8.8.8)",
		"Cloudflare (1.1.1.1)",
		"Yandex (77.88.8.8)",
		"Quad9 (9.9.9.9)",
		"AdGuard (94.140.14.14)",
		`name="dns_preset"`,
		`name="dns_custom"`,
		"dnsPreset === 'custom'",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("instance DNS selector is missing %q", required)
		}
	}
}

func TestInstanceFormScopesWBTokenAndCustomDNSFields(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	start := strings.Index(source, "function openInstanceForm")
	end := strings.Index(source, "function normalizeRoomID")
	if start < 0 || end <= start {
		t.Fatal("instance form source boundaries are missing")
	}
	form := source[start:end]
	for _, required := range []string{
		"transport:'vp8channel'",
		`data-role="auth-token-row"`,
		`data-role="omit-token-row"`,
		"i.provider === 'wbstream'",
		"syncInstanceFormFields(document.querySelector('form[data-form=\"instance\"]'))",
		`data-role="dns-custom-row"`,
		"dnsPreset === 'custom'",
	} {
		if !strings.Contains(form, required) {
			t.Fatalf("instance form is missing %q", required)
		}
	}
	if strings.Contains(form, "Для WB QR OLCRTC Client содержит полный auth token") {
		t.Fatal("obsolete WB QR warning is still shown in the instance form")
	}
	if !strings.Contains(source, "event.target.dataset.role === 'dns-preset' || event.target.name === 'provider'") {
		t.Fatal("instance form visibility is not synchronized on provider/DNS changes")
	}
}

func TestSidebarLinksToPanelRepository(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	if !strings.Contains(source, `href="https://github.com/juushimatsu/olcrtc-panel-lite"`) || !strings.Contains(source, `rel="noopener noreferrer"`) {
		t.Fatal("sidebar repository link is missing or unsafe")
	}
}

func TestDashboardVisibilityUpdateNoticeAndBulkMirrorActions(t *testing.T) {
	app, err := fs.ReadFile(Static, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := fs.ReadFile(Static, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, required := range []string{
		"function networkVisibilityButton()",
		"&#128065;",
		"function loadUpdateNotice()",
		"data-action=\"dismiss-update-notice\"",
		"state.updateNoticeDismissed",
		"function syncAllMirrors()",
		"data-action=\"sync-mirror-all\"",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("UI feature is missing %q", required)
		}
	}
	if !strings.Contains(string(styles), ".update-notice {") {
		t.Fatal("update notice styles are missing")
	}
}
