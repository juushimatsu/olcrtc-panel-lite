// Package instance validates, renders and manages official olcRTC configurations.
package instance

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
	"gopkg.in/yaml.v3"
)

var providers = map[string]bool{"jitsi": true, "telemost": true, "wbstream": true}
var transports = map[string]bool{"datachannel": true, "vp8channel": true, "seichannel": true, "videochannel": true}

// Proxy is the normalized official upstream SOCKS proxy block.
type Proxy struct {
	Addr string
	Port int
	User string
	Pass string
}

// ApplyDefaults fills values from the current official upstream defaults.
func ApplyDefaults(item *model.Instance) {
	if item.DNS == "" {
		item.DNS = "8.8.8.8:53"
	}
	if item.ResetPolicy == "" {
		item.ResetPolicy = "never"
	}
	if item.Liveness.Interval == "" {
		item.Liveness.Interval = "10s"
	}
	if item.Liveness.Timeout == "" {
		item.Liveness.Timeout = "5s"
	}
	if item.Liveness.Failures == 0 {
		item.Liveness.Failures = 3
	}
	if item.Options.VP8FPS == 0 {
		item.Options.VP8FPS = 30
	}
	if item.Options.VP8Batch == 0 {
		item.Options.VP8Batch = 64
	}
	if item.Options.SEIFPS == 0 {
		item.Options.SEIFPS = 30
	}
	if item.Options.SEIBatch == 0 {
		item.Options.SEIBatch = 64
	}
	if item.Options.SEIFragment == 0 {
		item.Options.SEIFragment = 900
	}
	if item.Options.SEIAckMS == 0 {
		item.Options.SEIAckMS = 2000
	}
	if item.Options.VideoCodec == "" {
		item.Options.VideoCodec = "qrcode"
	}
	if item.Options.VideoWidth == 0 {
		item.Options.VideoWidth = 1920
	}
	if item.Options.VideoHeight == 0 {
		item.Options.VideoHeight = 1080
	}
	if item.Options.VideoFPS == 0 {
		item.Options.VideoFPS = 30
	}
	if item.Options.VideoBitrate == "" {
		item.Options.VideoBitrate = "2M"
	}
	if item.Options.VideoHW == "" {
		item.Options.VideoHW = "none"
	}
	if item.Options.VideoQRRecover == "" {
		item.Options.VideoQRRecover = "low"
	}
	if item.Options.VideoTile == 0 {
		item.Options.VideoTile = 4
	}
	if item.Options.VideoTileRS == 0 {
		item.Options.VideoTileRS = 20
	}
}

// Validate checks fields before they can reach YAML, paths or systemd.
func Validate(item model.Instance) error {
	if strings.TrimSpace(item.Name) == "" || len([]rune(item.Name)) > 80 || strings.ContainsAny(item.Name, "\r\n") {
		return errors.New("name is required and must be a single line up to 80 characters")
	}
	if !providers[item.Provider] {
		return errors.New("unsupported provider")
	}
	if !transports[item.Transport] {
		return errors.New("unsupported transport")
	}
	if strings.TrimSpace(item.RoomID) == "" || strings.ContainsAny(item.RoomID, "\r\n?<>@#$\x00") {
		return errors.New("room_id is empty or contains a reserved separator")
	}
	if item.Provider == "jitsi" {
		if err := validateJitsiRoom(item.RoomID); err != nil {
			return err
		}
	}
	if _, _, err := net.SplitHostPort(item.DNS); err != nil {
		return fmt.Errorf("invalid DNS address: %w", err)
	}
	if item.OutboundProxy != "" {
		if _, err := ParseProxy(item.OutboundProxy); err != nil {
			return err
		}
	}
	if err := validateDuration(item.Liveness.Interval, true); err != nil {
		return fmt.Errorf("invalid liveness interval: %w", err)
	}
	if err := validateDuration(item.Liveness.Timeout, true); err != nil {
		return fmt.Errorf("invalid liveness timeout: %w", err)
	}
	if item.Liveness.Failures < 1 || item.Liveness.Failures > 100 {
		return errors.New("liveness failures must be in range 1..100")
	}
	if item.MaxSessionDuration != "" {
		if err := validateDuration(item.MaxSessionDuration, true); err != nil {
			return fmt.Errorf("invalid max session duration: %w", err)
		}
	}
	if item.Traffic.MinDelay != "" {
		if err := validateDuration(item.Traffic.MinDelay, false); err != nil {
			return fmt.Errorf("invalid minimum traffic delay: %w", err)
		}
	}
	if item.Traffic.MaxDelay != "" {
		if err := validateDuration(item.Traffic.MaxDelay, false); err != nil {
			return fmt.Errorf("invalid maximum traffic delay: %w", err)
		}
	}
	if item.QuotaBytes < 0 {
		return errors.New("quota must not be negative")
	}
	switch item.ResetPolicy {
	case "never", "daily", "weekly", "monthly", "manual":
	default:
		return errors.New("unsupported reset policy")
	}
	return validateTransport(item)
}

func validateJitsiRoom(room string) error {
	value := room
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("Jitsi room must be an http/https room URL")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("Jitsi room URL must not contain credentials, query or fragment")
	}
	return nil
}

func validateDuration(value string, positive bool) error {
	d, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	if positive && d <= 0 {
		return errors.New("duration must be positive")
	}
	if !positive && d < 0 {
		return errors.New("duration must not be negative")
	}
	return nil
}

func validateTransport(item model.Instance) error {
	o := item.Options
	switch item.Transport {
	case "vp8channel":
		if o.VP8FPS < 1 || o.VP8FPS > 240 || o.VP8Batch < 1 || o.VP8Batch > 4096 {
			return errors.New("VP8 settings are outside safe ranges")
		}
	case "seichannel":
		if o.SEIFPS < 1 || o.SEIFPS > 240 || o.SEIBatch < 1 || o.SEIBatch > 4096 || o.SEIFragment < 128 || o.SEIFragment > 65535 || o.SEIAckMS < 100 || o.SEIAckMS > 120000 {
			return errors.New("SEI settings are outside safe ranges")
		}
	case "videochannel":
		if o.VideoBitrate == "" {
			return errors.New("video bitrate is required")
		}
		for _, r := range o.VideoBitrate {
			if (r < '0' || r > '9') && !strings.ContainsRune("kKmM", r) {
				return errors.New("video bitrate has invalid format")
			}
		}
		if o.VideoCodec != "qrcode" && o.VideoCodec != "tile" {
			return errors.New("video codec must be qrcode or tile")
		}
		if o.VideoWidth < 64 || o.VideoWidth > 7680 || o.VideoHeight < 64 || o.VideoHeight > 4320 || o.VideoFPS < 1 || o.VideoFPS > 240 {
			return errors.New("video dimensions or FPS are outside safe ranges")
		}
		if o.VideoHW != "none" && o.VideoHW != "nvenc" {
			return errors.New("video hardware mode must be none or nvenc")
		}
		if o.VideoCodec == "tile" && (o.VideoWidth != 1080 || o.VideoHeight != 1080 || o.VideoTile < 1 || o.VideoTile > 270 || o.VideoTileRS < 0 || o.VideoTileRS > 200) {
			return errors.New("tile codec requires 1080x1080 and valid tile settings")
		}
	}
	return nil
}

// CompatibilityWarning returns upstream matrix guidance for unstable combinations.
func CompatibilityWarning(provider, transport string) string {
	key := provider + ":" + transport
	switch key {
	case "telemost:datachannel", "telemost:seichannel":
		return "Эта комбинация не поддерживается upstream."
	case "telemost:videochannel":
		return "Videochannel через Telemost работает медленно и нестабильно."
	case "wbstream:datachannel":
		return "В гостевом WB-потоке datachannel не передаёт данные без moderator token."
	case "jitsi:vp8channel", "jitsi:seichannel", "jitsi:videochannel":
		return "Видео-транспорты Jitsi нестабильны; для надёжности используйте datachannel."
	default:
		return ""
	}
}

// ParseProxy accepts the documented SOCKS5 input forms.
func ParseProxy(value string) (Proxy, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Proxy{}, nil
	}
	if !strings.Contains(value, "://") {
		value = "socks5://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "socks5" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return Proxy{}, errors.New("proxy must use a valid socks5 host:port form")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return Proxy{}, errors.New("proxy port must be in range 1..65535")
	}
	proxy := Proxy{Addr: u.Hostname(), Port: port}
	if u.User != nil {
		proxy.User = u.User.Username()
		proxy.Pass, _ = u.User.Password()
		if strings.ContainsAny(proxy.User+proxy.Pass, "\r\n") {
			return Proxy{}, errors.New("proxy credentials must be a single line")
		}
	}
	return proxy, nil
}

type yamlConfig struct {
	Mode      string         `yaml:"mode"`
	Auth      yamlAuth       `yaml:"auth"`
	Room      yamlRoom       `yaml:"room"`
	Crypto    yamlCrypto     `yaml:"crypto"`
	Net       yamlNet        `yaml:"net"`
	SOCKS     *yamlSOCKS     `yaml:"socks,omitempty"`
	Video     *yamlVideo     `yaml:"video,omitempty"`
	VP8       *yamlVP8       `yaml:"vp8,omitempty"`
	SEI       *yamlSEI       `yaml:"sei,omitempty"`
	Liveness  yamlLiveness   `yaml:"liveness"`
	Lifecycle *yamlLifecycle `yaml:"lifecycle,omitempty"`
	Traffic   *yamlTraffic   `yaml:"traffic,omitempty"`
	Data      string         `yaml:"data"`
	Debug     bool           `yaml:"debug"`
}

type yamlAuth struct {
	Provider string `yaml:"provider"`
	Token    string `yaml:"token,omitempty"`
}
type yamlRoom struct {
	ID      string `yaml:"id"`
	Channel string `yaml:"channel,omitempty"`
}
type yamlCrypto struct {
	KeyFile string `yaml:"key_file"`
}
type yamlNet struct {
	Transport string `yaml:"transport"`
	DNS       string `yaml:"dns"`
}
type yamlSOCKS struct {
	ProxyAddr string `yaml:"proxy_addr"`
	ProxyPort int    `yaml:"proxy_port"`
	ProxyUser string `yaml:"proxy_user,omitempty"`
	ProxyPass string `yaml:"proxy_pass,omitempty"`
}
type yamlVP8 struct {
	FPS       int `yaml:"fps"`
	BatchSize int `yaml:"batch_size"`
}
type yamlSEI struct {
	FPS          int `yaml:"fps"`
	BatchSize    int `yaml:"batch_size"`
	FragmentSize int `yaml:"fragment_size"`
	AckTimeoutMS int `yaml:"ack_timeout_ms"`
}
type yamlVideo struct {
	Codec      string `yaml:"codec"`
	Width      int    `yaml:"width"`
	Height     int    `yaml:"height"`
	FPS        int    `yaml:"fps"`
	Bitrate    string `yaml:"bitrate"`
	HW         string `yaml:"hw"`
	QRSize     int    `yaml:"qr_size,omitempty"`
	QRRecovery string `yaml:"qr_recovery"`
	TileModule int    `yaml:"tile_module,omitempty"`
	TileRS     int    `yaml:"tile_rs,omitempty"`
}
type yamlLiveness struct {
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
	Failures int    `yaml:"failures"`
}
type yamlLifecycle struct {
	MaxSessionDuration string `yaml:"max_session_duration"`
}
type yamlTraffic struct {
	MaxPayloadSize int    `yaml:"max_payload_size,omitempty"`
	MinDelay       string `yaml:"min_delay,omitempty"`
	MaxDelay       string `yaml:"max_delay,omitempty"`
}

// RenderYAML serializes only fields supported by official upstream.
func RenderYAML(item model.Instance, keyPath, dataPath string) ([]byte, error) {
	if err := Validate(item); err != nil {
		return nil, err
	}
	cfg := yamlConfig{
		Mode: "srv", Auth: yamlAuth{Provider: item.Provider, Token: item.AuthToken},
		Room:   yamlRoom{ID: item.RoomID, Channel: item.RoomChannel},
		Crypto: yamlCrypto{KeyFile: keyPath}, Net: yamlNet{Transport: item.Transport, DNS: item.DNS},
		Liveness: yamlLiveness{Interval: item.Liveness.Interval, Timeout: item.Liveness.Timeout, Failures: item.Liveness.Failures},
		Data:     dataPath, Debug: item.Debug,
	}
	if item.OutboundProxy != "" {
		proxy, err := ParseProxy(item.OutboundProxy)
		if err != nil {
			return nil, err
		}
		cfg.SOCKS = &yamlSOCKS{ProxyAddr: proxy.Addr, ProxyPort: proxy.Port, ProxyUser: proxy.User, ProxyPass: proxy.Pass}
	}
	switch item.Transport {
	case "vp8channel":
		cfg.VP8 = &yamlVP8{FPS: item.Options.VP8FPS, BatchSize: item.Options.VP8Batch}
	case "seichannel":
		cfg.SEI = &yamlSEI{FPS: item.Options.SEIFPS, BatchSize: item.Options.SEIBatch, FragmentSize: item.Options.SEIFragment, AckTimeoutMS: item.Options.SEIAckMS}
	case "videochannel":
		cfg.Video = &yamlVideo{Codec: item.Options.VideoCodec, Width: item.Options.VideoWidth, Height: item.Options.VideoHeight, FPS: item.Options.VideoFPS, Bitrate: item.Options.VideoBitrate, HW: item.Options.VideoHW, QRSize: item.Options.VideoQRSize, QRRecovery: item.Options.VideoQRRecover, TileModule: item.Options.VideoTile, TileRS: item.Options.VideoTileRS}
	}
	if item.MaxSessionDuration != "" {
		cfg.Lifecycle = &yamlLifecycle{MaxSessionDuration: item.MaxSessionDuration}
	}
	if item.Traffic.MaxPayloadSize != 0 || item.Traffic.MinDelay != "" || item.Traffic.MaxDelay != "" {
		cfg.Traffic = &yamlTraffic{MaxPayloadSize: item.Traffic.MaxPayloadSize, MinDelay: item.Traffic.MinDelay, MaxDelay: item.Traffic.MaxDelay}
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal olcrtc YAML: %w", err)
	}
	var check map[string]any
	if err := yaml.Unmarshal(b, &check); err != nil {
		return nil, fmt.Errorf("verify generated YAML: %w", err)
	}
	return b, nil
}
