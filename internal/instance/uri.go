package instance

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
)

// StandardURI renders docs/uri.md exactly, without private auth tokens.
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

// ExclaveURI renders the isolated compatibility projection.
func ExclaveURI(item model.Instance, key, name string) (string, error) {
	if err := validateURIFields(item, key, name); err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("key", key)
	query.Set("transport", item.Transport)
	if item.DNS != "" {
		query.Set("dns", item.DNS)
	}
	switch item.Transport {
	case "vp8channel":
		query.Set("vp8_fps", strconv.Itoa(item.Options.VP8FPS))
		query.Set("vp8_batch", strconv.Itoa(item.Options.VP8Batch))
	case "seichannel":
		query.Set("sei_fps", strconv.Itoa(item.Options.SEIFPS))
		query.Set("sei_batch", strconv.Itoa(item.Options.SEIBatch))
		query.Set("sei_fragment", strconv.Itoa(item.Options.SEIFragment))
		query.Set("sei_ack_ms", strconv.Itoa(item.Options.SEIAckMS))
	}
	return "olcrtc://" + item.Provider + "@room/" + url.PathEscape(item.RoomID) + "?" + query.Encode() + "#" + url.QueryEscape(name), nil
}

func validateURIFields(item model.Instance, key, comment string) error {
	if len(key) != 64 {
		return errors.New("encryption key must contain exactly 64 hex characters")
	}
	for _, r := range key {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return errors.New("encryption key must be hexadecimal")
		}
	}
	if !providers[item.Provider] || !transports[item.Transport] {
		return errors.New("unsupported URI provider or transport")
	}
	if strings.ContainsAny(item.Provider+item.Transport, "?<>@#$\r\n") || strings.ContainsAny(item.RoomID, "?<>@#$\r\n") || strings.ContainsAny(comment, "?<>@#$\r\n") {
		return fmt.Errorf("uri field contains a reserved separator")
	}
	return nil
}
