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
