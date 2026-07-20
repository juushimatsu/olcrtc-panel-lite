package instance

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
)

// StandardURI renders the OLCBOX URI format without private auth tokens.
func StandardURI(item model.Instance, key, comment string) (string, error) {
	if err := validateURIFields(item, key, comment); err != nil {
		return "", err
	}
	payload := standardPayload(item)
	return "olcrtc://" + item.Provider + "?" + item.Transport + payload + "@" + item.RoomID + "#" + key + "$" + comment, nil
}

func standardPayload(item model.Instance) string {
	values := make(map[string]string)
	switch item.Transport {
	case "vp8channel":
		if item.Options.VP8FPS != 30 {
			values["vp8-fps"] = strconv.Itoa(item.Options.VP8FPS)
		}
		if item.Options.VP8Batch != 64 {
			values["vp8-batch"] = strconv.Itoa(item.Options.VP8Batch)
		}
	case "seichannel":
		if item.Options.SEIFPS != 30 {
			values["fps"] = strconv.Itoa(item.Options.SEIFPS)
		}
		if item.Options.SEIBatch != 64 {
			values["batch"] = strconv.Itoa(item.Options.SEIBatch)
		}
		if item.Options.SEIFragment != 900 {
			values["frag"] = strconv.Itoa(item.Options.SEIFragment)
		}
		if item.Options.SEIAckMS != 2000 {
			values["ack-ms"] = strconv.Itoa(item.Options.SEIAckMS)
		}
	case "videochannel":
		if item.Options.VideoWidth != 1920 {
			values["video-w"] = strconv.Itoa(item.Options.VideoWidth)
		}
		if item.Options.VideoHeight != 1080 {
			values["video-h"] = strconv.Itoa(item.Options.VideoHeight)
		}
		if item.Options.VideoFPS != 30 {
			values["video-fps"] = strconv.Itoa(item.Options.VideoFPS)
		}
		if item.Options.VideoBitrate != "2M" {
			values["video-bitrate"] = item.Options.VideoBitrate
		}
		if item.Options.VideoHW != "none" {
			values["video-hw"] = item.Options.VideoHW
		}
		if item.Options.VideoCodec != "qrcode" {
			values["video-codec"] = item.Options.VideoCodec
		}
		if item.Options.VideoQRSize != 0 {
			values["video-qr-size"] = strconv.Itoa(item.Options.VideoQRSize)
		}
		if item.Options.VideoQRRecover != "low" {
			values["video-qr-recovery"] = item.Options.VideoQRRecover
		}
		if item.Options.VideoCodec == "tile" {
			if item.Options.VideoTile != 4 {
				values["video-tile-module"] = strconv.Itoa(item.Options.VideoTile)
			}
			if item.Options.VideoTileRS != 20 {
				values["video-tile-rs"] = strconv.Itoa(item.Options.VideoTileRS)
			}
		}
	}
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return "<" + strings.Join(parts, "&") + ">"
}

// ClientCompatible reports whether the Android OLCRTC Client implements a
// provider/transport pair.
func ClientCompatible(provider, transport string) bool {
	return (provider == "wbstream" || provider == "telemost") && transport == "vp8channel" ||
		provider == "jitsi" && transport == "datachannel"
}

// ClientURI renders the compact secret-bearing URI understood by OLCRTC
// Client. The WB account token is intentionally included in full.
func ClientURI(item model.Instance, key, name string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if !ClientCompatible(item.Provider, item.Transport) {
		return "", fmt.Errorf("OLCRTC Client не поддерживает %s + %s", item.Provider, item.Transport)
	}
	if strings.TrimSpace(item.RoomID) == "" || strings.ContainsAny(item.RoomID, "\r\n") {
		return "", errors.New("room ID is required and must be single-line")
	}
	if strings.TrimSpace(item.ClientID) == "" || strings.ContainsAny(item.ClientID, "\r\n") {
		return "", errors.New("client_id is required")
	}
	if item.Provider == "wbstream" && item.AuthToken == "" {
		return "", errors.New("WB auth token is required for OLCRTC Client QR")
	}

	parts := []string{"k=" + clientEscape(key), "t=" + clientEscape(item.Transport)}
	if item.Transport == "vp8channel" {
		parts = append(parts, "f="+strconv.Itoa(item.Options.VP8FPS), "b="+strconv.Itoa(item.Options.VP8Batch))
	}
	parts = append(parts, "c="+clientEscape(item.ClientID))
	if item.Provider == "wbstream" {
		parts = append(parts, "a="+clientEscape(item.AuthToken))
	}
	if item.DNS != "" {
		parts = append(parts, "d="+clientEscape(item.DNS))
	}
	fragment := ""
	if name != "" {
		fragment = "#" + clientEscape(name)
	}
	return "olcrtc://" + item.Provider + "@r/" + clientEscape(item.RoomID) + "?" + strings.Join(parts, "&") + fragment, nil
}

// ValidateClientURI rejects manual subscription entries which OLCRTC Client
// cannot parse or connect with.
func ValidateClientURI(raw string) error {
	if len(raw) > 16*1024 {
		return errors.New("OLCRTC Client URI exceeds 16 KiB")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "olcrtc") {
		return errors.New("manual URI must use olcrtc://")
	}
	provider := ""
	if u.User != nil {
		provider = strings.ToLower(u.User.Username())
		if _, hasPassword := u.User.Password(); hasPassword {
			return errors.New("OLCRTC Client URI userinfo must contain only provider")
		}
	}
	if provider == "" || u.Host == "" {
		return errors.New("OLCRTC Client URI must contain provider and room host")
	}

	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("OLCRTC Client URI query is invalid")
	}
	allowed := map[string]bool{
		"key": true, "k": true, "transport": true, "t": true,
		"vp8_fps": true, "f": true, "vp8_batch": true, "b": true,
		"client_id": true, "c": true, "auth_token": true, "auth.token": true, "a": true,
		"dns": true, "d": true, "room_password": true, "rp": true, "keepalive": true, "ka": true,
	}
	for name, entries := range values {
		if !allowed[name] || len(entries) != 1 {
			return fmt.Errorf("unsupported or duplicate OLCRTC Client parameter %q", name)
		}
	}
	parameter := func(names ...string) (string, error) {
		found := ""
		count := 0
		for _, name := range names {
			if entries, ok := values[name]; ok {
				found = entries[0]
				count++
			}
		}
		if count > 1 {
			return "", fmt.Errorf("duplicate OLCRTC Client aliases for %s", names[0])
		}
		return found, nil
	}
	key, err := parameter("key", "k")
	if err != nil || key == "" {
		return errors.New("OLCRTC Client URI requires key")
	}
	if err := validateKey(key); err != nil {
		return err
	}
	clientID, err := parameter("client_id", "c")
	if err != nil || strings.TrimSpace(clientID) == "" {
		return errors.New("OLCRTC Client URI requires client_id")
	}
	transport, err := parameter("transport", "t")
	if err != nil {
		return err
	}
	if transport == "" {
		transport = "datachannel"
	}
	if !ClientCompatible(provider, strings.ToLower(transport)) {
		return fmt.Errorf("OLCRTC Client не поддерживает %s + %s", provider, transport)
	}
	for _, aliases := range [][]string{{"auth_token", "auth.token", "a"}, {"room_password", "rp"}} {
		if _, err := parameter(aliases...); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		names []string
		min   int
		max   int
	}{{[]string{"vp8_fps", "f"}, 1, 120}, {[]string{"vp8_batch", "b"}, 1, 64}, {[]string{"keepalive", "ka"}, 0, 3600}} {
		value, valueErr := parameter(field.names...)
		if valueErr != nil {
			return valueErr
		}
		if value != "" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil || parsed < field.min || parsed > field.max {
				return fmt.Errorf("OLCRTC Client parameter %s must be in %d..%d", field.names[0], field.min, field.max)
			}
		}
	}
	dns, err := parameter("dns", "d")
	if err != nil {
		return err
	}
	if dns != "" {
		host, port, splitErr := net.SplitHostPort(dns)
		portNumber, portErr := strconv.Atoi(port)
		if splitErr != nil || net.ParseIP(host) == nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
			return errors.New("OLCRTC Client DNS must be a numeric IP:port")
		}
	}
	return nil
}

func clientEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func validateURIFields(item model.Instance, key, comment string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if !providers[item.Provider] || !transports[item.Transport] {
		return errors.New("unsupported URI provider or transport")
	}
	if strings.ContainsAny(item.Provider+item.Transport, "?<>@#$\r\n") || strings.ContainsAny(item.RoomID, "?<>@#$\r\n") || strings.ContainsAny(comment, "?<>@#$\r\n") {
		return fmt.Errorf("uri field contains a reserved separator")
	}
	return nil
}

func validateKey(key string) error {
	if len(key) != 64 {
		return errors.New("encryption key must contain exactly 64 hex characters")
	}
	for _, r := range key {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return errors.New("encryption key must be hexadecimal")
		}
	}
	return nil
}
