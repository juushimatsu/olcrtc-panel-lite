package redact

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	input := "Authorization: Bearer top-secret\nauth_token=another\nolcrtc://jitsi?datachannel@room#" + strings.Repeat("a", 64) + "$node"
	got := Text(input)
	for _, secret := range []string{"top-secret", "another", strings.Repeat("a", 64)} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked in %q", secret, got)
		}
	}
}
