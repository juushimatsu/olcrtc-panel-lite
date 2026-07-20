package instance

import (
	"strings"
	"testing"
)

func TestURIFormatDistinguishesClientAndOLCBOX(t *testing.T) {
	key := strings.Repeat("a", 64)
	client := "olcrtc://jitsi@r/https%3A%2F%2Fmeet.example%2Froom?k=" + key + "&t=datachannel&c=client-id"
	olcbox := "olcrtc://jitsi?datachannel@https://meet.example/room#" + key + "$node"
	if got := URIFormat(client); got != "client" {
		t.Fatalf("client format = %q", got)
	}
	if got := URIFormat(olcbox); got != "olcbox" {
		t.Fatalf("OLCBOX format = %q", got)
	}
}

func TestValidateStandardURI(t *testing.T) {
	key := strings.Repeat("b", 64)
	valid := "olcrtc://wbstream?seichannel<fps=60&batch=64&frag=900&ack-ms=2000>@room-01#" + key + "$RU / free"
	if err := ValidateStandardURI(valid); err != nil {
		t.Fatalf("valid OLCBOX URI rejected: %v", err)
	}
	for _, raw := range []string{
		"olcrtc://jitsi?datachannel@room#short",
		"olcrtc://jitsi?unknown@room#" + key,
		"olcrtc://jitsi?datachannel<bad=x>@room#" + key,
		"olcrtc://jitsi@r/room?k=" + key + "&t=datachannel&c=id",
	} {
		if err := ValidateStandardURI(raw); err == nil {
			t.Fatalf("invalid OLCBOX URI accepted: %s", raw)
		}
	}
}
