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

func TestTextRedactsCompactClientSecrets(t *testing.T) {
	input := `uri=olcrtc://wbstream@r/room?k=key&t=vp8channel&c=client&a=full.jwt.token&d=1.1.1.1%3A53 payload={"mk":"mirror-secret"}`
	got := Text(input)
	for _, secret := range []string{"full.jwt.token", "mirror-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("compact secret %q leaked in %q", secret, got)
		}
	}
}
