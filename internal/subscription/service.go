// Package subscription renders standard and compatibility subscriptions.
package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/instance"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/mirror"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/security"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/store"
)

// Service resolves linked entries at request time.
type Service struct {
	store     *store.Store
	instances *instance.Manager
	secrets   *security.Secrets
	mu        sync.RWMutex
	baseURL   string
}

// NewService creates a renderer.
func NewService(st *store.Store, instances *instance.Manager, secrets *security.Secrets, baseURL string) *Service {
	return &Service{store: st, instances: instances, secrets: secrets, baseURL: strings.TrimRight(baseURL, "/")}
}

// SetBaseURL updates generated subscription QR URLs after IP or port changes.
func (s *Service) SetBaseURL(value string) {
	s.mu.Lock()
	s.baseURL = strings.TrimRight(value, "/")
	s.mu.Unlock()
}

// Standard renders docs/sub.md and returns aggregate traffic.
func (s *Service) Standard(ctx context.Context, slug string) (string, model.Subscription, error) {
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return "", model.Subscription{}, err
	}
	if !sub.Enabled {
		return "", sub, ErrDisabled
	}
	lines := []string{"#name: " + safeLine(sub.Name), "#update: " + strconv.FormatInt(sub.UpdatedAt.Unix(), 10), "#refresh: " + safeLine(sub.RefreshInterval)}
	if sub.Color != "" {
		lines = append(lines, "#color: "+safeLine(sub.Color))
	}
	if sub.Icon != "" {
		lines = append(lines, "#icon: "+safeLine(sub.Icon))
	}
	aggregate, err := s.aggregate(ctx, sub)
	if err != nil {
		return "", sub, err
	}
	sub.UsedBytes = aggregate.used
	sub.UploadBytes = aggregate.upload
	sub.DownloadBytes = aggregate.download
	sub.ExpiresAt = aggregate.expires
	if aggregate.allLimited {
		sub.AvailableBytes = &aggregate.available
	}
	lines = append(lines, "#used: "+humanBytes(aggregate.used))
	if aggregate.allLimited {
		lines = append(lines, "#available: "+humanBytes(aggregate.available))
	} else {
		lines = append(lines, "#available: unlimited")
	}
	lines = append(lines, "")
	for _, entry := range sub.Entries {
		if !entry.Enabled {
			continue
		}
		uri, source, err := s.resolveEntry(ctx, entry, "standard")
		if err != nil {
			return "", sub, err
		}
		lines = append(lines, uri)
		appendEntryMetadata(&lines, entry, source)
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n", sub, nil
}

// Exclave renders one compatibility URI per line.
func (s *Service) Exclave(ctx context.Context, slug string) (string, model.Subscription, error) {
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return "", model.Subscription{}, err
	}
	if !sub.Enabled {
		return "", sub, ErrDisabled
	}
	lines := make([]string, 0, len(sub.Entries))
	for _, entry := range sub.Entries {
		if !entry.Enabled || (entry.SourceInstanceID == nil && !entry.ExclaveCompatible) {
			continue
		}
		uri, _, err := s.resolveEntry(ctx, entry, "exclave")
		if err != nil {
			return "", sub, err
		}
		lines = append(lines, uri)
	}
	return strings.Join(lines, "\n") + "\n", sub, nil
}

// Bundle returns the compatibility QR JSON without leaking server-only tokens.
func (s *Service) Bundle(ctx context.Context, slug string) ([]byte, error) {
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return nil, err
	}
	key := ""
	mirrors := make([]map[string]any, 0, 1)
	if sub.MirrorEnabled {
		encryptedKey, err := s.store.SubscriptionMirrorKey(ctx, sub.ID)
		if err != nil {
			return nil, err
		}
		plainKey, err := s.secrets.Decrypt(encryptedKey)
		if err != nil {
			return nil, err
		}
		key = plainKey
		if sub.MirrorPublicURL != "" {
			mirrors = append(mirrors, map[string]any{"t": "yandex_disk", "u": sub.MirrorPublicURL, "e": true, "a": "AES-256-GCM"})
		}
	}
	s.mu.RLock()
	baseURL := s.baseURL
	s.mu.RUnlock()
	bundle := map[string]any{"type": "olcrtc-sub", "v": 2, "n": sub.Name, "s": sub.Slug, "u": baseURL + "/sub/" + sub.Slug + "/exclave", "m": mirrors, "mk": key, "uc": true, "d": true}
	return json.Marshal(bundle)
}

// SyncMirror encrypts and uploads the legacy projection.
func (s *Service) SyncMirror(ctx context.Context, slug string) (string, error) {
	enabled, err := s.store.SettingOrDefault(ctx, "yandex_enabled", "false")
	if err != nil || enabled != "true" {
		return "", errors.New("Yandex mirror is disabled globally")
	}
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return "", err
	}
	if !sub.MirrorEnabled {
		return "", errors.New("mirror is disabled for this subscription")
	}
	legacy, _, err := s.Exclave(ctx, slug)
	if err != nil {
		return "", err
	}
	encryptedKey, err := s.store.SubscriptionMirrorKey(ctx, sub.ID)
	if err != nil {
		return "", err
	}
	keyB64, err := s.secrets.Decrypt(encryptedKey)
	if err != nil {
		return "", err
	}
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", errors.New("invalid stored mirror key")
	}
	payload, err := mirror.Encrypt(key, []byte(legacy))
	if err != nil {
		return "", err
	}
	tokenEncrypted, _, err := s.store.Setting(ctx, "yandex_oauth_token")
	if err != nil {
		return "", errors.New("Yandex OAuth token is not configured")
	}
	token, err := s.secrets.Decrypt(tokenEncrypted)
	if err != nil {
		return "", err
	}
	basePath, err := s.store.SettingOrDefault(ctx, "yandex_base_path", "/olcrtc/subscriptions")
	if err != nil {
		return "", err
	}
	client := mirror.NewClient(token, basePath)
	publicURL, err := client.Upload(ctx, slug, payload)
	if err != nil {
		_ = s.store.SetSubscriptionMirror(ctx, sub.ID, sub.MirrorPublicURL, "error")
		return "", err
	}
	if err := s.store.SetSubscriptionMirror(ctx, sub.ID, publicURL, "synced"); err != nil {
		return "", err
	}
	return publicURL, nil
}

// GenerateMirrorKey creates and encrypts a per-subscription key.
func (s *Service) GenerateMirrorKey() (string, string, error) {
	key, err := mirror.GenerateKey()
	if err != nil {
		return "", "", err
	}
	plain := base64.RawURLEncoding.EncodeToString(key)
	encrypted, err := s.secrets.Encrypt(plain)
	return plain, encrypted, err
}

func (s *Service) resolveEntry(ctx context.Context, entry model.SubscriptionEntry, format string) (string, *model.Instance, error) {
	if entry.SourceInstanceID == nil {
		return entry.RawURI, nil, nil
	}
	uri, err := s.instances.URI(ctx, *entry.SourceInstanceID, format)
	if err != nil {
		return "", nil, err
	}
	item, err := s.instances.Get(ctx, *entry.SourceInstanceID)
	if err != nil {
		return "", nil, err
	}
	return uri, &item, nil
}

type trafficAggregate struct {
	used       int64
	upload     int64
	download   int64
	available  int64
	allLimited bool
	expires    *time.Time
}

func (s *Service) aggregate(ctx context.Context, sub model.Subscription) (trafficAggregate, error) {
	result := trafficAggregate{allLimited: true}
	for _, entry := range sub.Entries {
		if !entry.Enabled {
			continue
		}
		if entry.SourceInstanceID != nil {
			item, err := s.instances.Get(ctx, *entry.SourceInstanceID)
			if err != nil {
				return trafficAggregate{}, err
			}
			result.used += item.TotalBytes
			result.upload += item.UploadBytes
			result.download += item.DownloadBytes
			if item.QuotaBytes > 0 {
				result.available += max(item.QuotaBytes-item.TotalBytes, 0)
			} else {
				result.allLimited = false
			}
			result.expires = earliest(result.expires, item.ExpiresAt)
			result.expires = earliest(result.expires, entry.ExpiresAt)
			continue
		}
		if entry.ManualUsed != nil {
			result.used += *entry.ManualUsed
			result.upload += *entry.ManualUsed
		}
		if entry.ManualAvailable != nil {
			result.available += *entry.ManualAvailable
		} else {
			result.allLimited = false
		}
		result.expires = earliest(result.expires, entry.ExpiresAt)
	}
	return result, nil
}

func earliest(current, candidate *time.Time) *time.Time {
	if candidate == nil {
		return current
	}
	if current == nil || candidate.Before(*current) {
		value := *candidate
		return &value
	}
	return current
}

func appendEntryMetadata(lines *[]string, entry model.SubscriptionEntry, source *model.Instance) {
	name := entry.Name
	if name == "" && source != nil {
		name = source.Name
	}
	if name != "" {
		*lines = append(*lines, "##name: "+safeLine(name))
	}
	if entry.Color != "" {
		*lines = append(*lines, "##color: "+safeLine(entry.Color))
	}
	if entry.Icon != "" {
		*lines = append(*lines, "##icon: "+safeLine(entry.Icon))
	}
	if entry.IP != "" {
		*lines = append(*lines, "##ip: "+safeLine(entry.IP))
	}
	if entry.Comment != "" {
		*lines = append(*lines, "##comment: "+safeLine(entry.Comment))
	}
	if source != nil {
		if source.QuotaBytes > 0 {
			*lines = append(*lines, "##used: "+humanBytes(source.TotalBytes)+"/"+humanBytes(source.QuotaBytes), "##available: "+humanBytes(max(source.QuotaBytes-source.TotalBytes, 0)))
		} else {
			*lines = append(*lines, "##used: "+humanBytes(source.TotalBytes), "##available: unlimited")
		}
	} else {
		if entry.ManualUsed != nil {
			*lines = append(*lines, "##used: "+humanBytes(*entry.ManualUsed))
		}
		if entry.ManualAvailable != nil {
			*lines = append(*lines, "##available: "+humanBytes(*entry.ManualAvailable))
		}
	}
}

func safeLine(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
}

func humanBytes(value int64) string {
	units := []string{"b", "kb", "mb", "gb", "tb", "pb"}
	n := float64(value)
	unit := 0
	for n >= 1024 && unit < len(units)-1 {
		n /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d%s", value, units[unit])
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", n), "0"), ".") + units[unit]
}

// TrafficHeader returns the optional standard Subscription-Userinfo value.
func TrafficHeader(sub model.Subscription) string {
	total := int64(0)
	if sub.AvailableBytes != nil {
		total = sub.UsedBytes + *sub.AvailableBytes
	}
	expires := int64(0)
	if sub.ExpiresAt != nil {
		expires = sub.ExpiresAt.Unix()
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", sub.UploadBytes, sub.DownloadBytes, total, expires)
}

// ErrDisabled maps to HTTP 410 for public routes.
var ErrDisabled = errors.New("subscription is disabled")

// Touch marks a subscription as updated after entry or linked instance changes.
func (s *Service) Touch(ctx context.Context, slug string) error {
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return err
	}
	sub.UpdatedAt = time.Now()
	_, err = s.store.UpdateSubscription(ctx, sub)
	return err
}
