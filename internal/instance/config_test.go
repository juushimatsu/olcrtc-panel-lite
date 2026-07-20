package instance

import (
	"net/url"
	"strings"
	"testing"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"gopkg.in/yaml.v3"
)

func validInstance() model.Instance {
	item := model.Instance{Name: "RU Jitsi", Provider: "jitsi", Transport: "vp8channel", RoomID: "https://meet.example/room", DNS: "8.8.8.8:53", ResetPolicy: "never"}
	ApplyDefaults(&item)
	return item
}

func TestStandardURIDefaultPayloadIsOmitted(t *testing.T) {
	item := validInstance()
	key := strings.Repeat("a", 64)
	got, err := StandardURI(item, key, "RU")
	if err != nil {
		t.Fatal(err)
	}
	want := "olcrtc://jitsi?vp8channel@https://meet.example/room#" + key + "$RU"
	if got != want {
		t.Fatalf("URI = %q, want %q", got, want)
	}
}

func TestStandardURITransportMapping(t *testing.T) {
	item := validInstance()
	item.Options.VP8FPS = 60
	item.Options.VP8Batch = 32
	got, err := StandardURI(item, strings.Repeat("b", 64), "node")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<vp8-batch=32&vp8-fps=60>") {
		t.Fatalf("payload mapping missing: %s", got)
	}
}

func TestURIRejectsReservedSeparator(t *testing.T) {
	item := validInstance()
	item.RoomID = "room#leak"
	if _, err := StandardURI(item, strings.Repeat("a", 64), "node"); err == nil {
		t.Fatal("reserved separator accepted")
	}
}

func TestClientURIContainsCompleteWBToken(t *testing.T) {
	item := validInstance()
	item.Provider = "wbstream"
	item.Transport = "vp8channel"
	item.RoomID = "11111111-2222-4333-8444-555555555555"
	item.ClientID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	item.AuthToken = strings.Repeat("header.payload.signature+", 80)
	got, err := ClientURI(item, strings.Repeat("c", 64), "node")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("a") != item.AuthToken {
		t.Fatalf("auth token was changed or truncated: got %d bytes, want %d", len(parsed.Query().Get("a")), len(item.AuthToken))
	}
	if parsed.Query().Get("c") != item.ClientID || parsed.Query().Get("k") != strings.Repeat("c", 64) {
		t.Fatalf("required client parameters missing: %s", got)
	}
}

func TestValidateClientURICompatibility(t *testing.T) {
	key := strings.Repeat("a", 64)
	valid := "olcrtc://jitsi@r/https%3A%2F%2Fmeet.example%2Froom?k=" + key + "&t=datachannel&c=aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if err := ValidateClientURI(valid); err != nil {
		t.Fatalf("valid client URI rejected: %v", err)
	}
	for _, invalid := range []string{
		"olcrtc://jitsi?datachannel@room#" + key + "$name",
		"olcrtc://jitsi@r/room?k=" + key + "&t=vp8channel&c=id",
		"olcrtc://jitsi@r/room?k=" + key + "&t=datachannel&c=id&a=one&auth_token=two",
		"olcrtc://jitsi@r/room?k=" + key + "&t=datachannel&c=id&ka=3601",
	} {
		if err := ValidateClientURI(invalid); err == nil {
			t.Fatalf("invalid client URI accepted: %s", invalid)
		}
	}
}

func TestRenderYAMLUsesOfficialFields(t *testing.T) {
	item := validInstance()
	item.OutboundProxy = "socks5://user:pass@127.0.0.1:40000"
	item.AuthToken = "wb-only"
	b, err := RenderYAML(item, "/etc/olcrtc-panel/instances/1/key.hex", "/var/lib/olcrtc/1/data")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := yaml.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["mode"] != "srv" || decoded["data"] != "/var/lib/olcrtc/1/data" {
		t.Fatalf("wrong YAML: %s", b)
	}
	for _, forbidden := range []string{"subscription", "warp", "client_id", "gen"} {
		if _, ok := decoded[forbidden]; ok {
			t.Fatalf("fork-only field %q found", forbidden)
		}
	}
	if !strings.Contains(string(b), "proxy_addr: 127.0.0.1") || !strings.Contains(string(b), "key_file:") {
		t.Fatalf("official proxy/key fields missing: %s", b)
	}
}

func TestParseProxy(t *testing.T) {
	proxy, err := ParseProxy("user:pass@127.0.0.1:40000")
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Addr != "127.0.0.1" || proxy.Port != 40000 || proxy.User != "user" || proxy.Pass != "pass" {
		t.Fatalf("proxy = %#v", proxy)
	}
	if _, err := ParseProxy("http://127.0.0.1:80"); err == nil {
		t.Fatal("HTTP proxy accepted for upstream SOCKS")
	}
}

func TestValidateRejectsYAMLInjection(t *testing.T) {
	item := validInstance()
	item.RoomID = "room\ndebug: true"
	if err := Validate(item); err == nil {
		t.Fatal("multiline room accepted")
	}
	item = validInstance()
	item.Name = "node\nother: value"
	if err := Validate(item); err == nil {
		t.Fatal("multiline name accepted")
	}
}
