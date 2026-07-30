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
		"/sub/${sub.slug}/olcbox",
		"payload?format=${format}",
		"OLCBOX URI — в OLCBOX feed",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("subscription UI is missing %q", required)
		}
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
