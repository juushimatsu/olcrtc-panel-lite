// Package subscription renders OLCRTC Client and OLCBOX subscriptions.
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

	"github.com/juushimatsu/olcrtc-panel-lite/internal/instance"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/mirror"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
)

// Service resolves linked entries at request time.
type Service struct {
	store     *store.Store
	instances *instance.Manager
	secrets   *security.Secrets
	mu        sync.RWMutex
	mirrorMu  sync.Mutex
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

// Standard renders the backwards-compatible OLCRTC Client feed and returns
// aggregate traffic. The format-specific renderer keeps incompatible manual
// entries out of the feed while preserving the existing endpoint.
func (s *Service) Standard(ctx context.Context, slug string) (string, model.Subscription, error) {
	return s.render(ctx, slug, "client")
}

// OLCBOX renders the plain-text sub.md feed consumed by OLCBOX.
func (s *Service) OLCBOX(ctx context.Context, slug string) (string, model.Subscription, error) {
	return s.render(ctx, slug, "olcbox")
}

// Summary resolves traffic for every enabled entry exactly once, independent
// of the target client format.  It is used by the administration list, where
// a subscription can intentionally contain both Client and OLCBOX entries.
func (s *Service) Summary(ctx context.Context, slug string) (model.Subscription, error) {
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return model.Subscription{}, err
	}
	if !sub.Enabled {
		return sub, ErrDisabled
	}
	resolved := make([]resolvedEntry, 0, len(sub.Entries))
	for _, entry := range sub.Entries {
		if !entry.Enabled {
			continue
		}
		if entry.SourceInstanceID != nil {
			item, err := s.instances.Get(ctx, *entry.SourceInstanceID)
			if err != nil {
				return model.Subscription{}, err
			}
			resolved = append(resolved, resolvedEntry{entry: entry, source: &item})
			continue
		}
		raw := strings.TrimSpace(entry.RawURI)
		if instance.ValidateClientURI(raw) != nil && instance.ValidateStandardURI(raw) != nil {
			continue
		}
		resolved = append(resolved, resolvedEntry{entry: entry, uri: raw})
	}
	aggregate, err := s.aggregateEntries(ctx, resolved)
	if err != nil {
		return model.Subscription{}, err
	}
	sub.UsedBytes = aggregate.used
	sub.UploadBytes = aggregate.upload
	sub.DownloadBytes = aggregate.download
	sub.ExpiresAt = aggregate.expires
	if aggregate.allLimited {
		sub.AvailableBytes = &aggregate.available
	} else {
		sub.AvailableBytes = nil
	}
	return sub, nil
}

type resolvedEntry struct {
	entry  model.SubscriptionEntry
	uri    string
	source *model.Instance
}

func (s *Service) render(ctx context.Context, slug, format string) (string, model.Subscription, error) {
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
	resolved := make([]resolvedEntry, 0, len(sub.Entries))
	for _, entry := range sub.Entries {
		if !entry.Enabled {
			continue
		}
		uri, source, included, err := s.resolveEntryForFormat(ctx, entry, format)
		if err != nil {
			return "", sub, err
		}
		if included {
			resolved = append(resolved, resolvedEntry{entry: entry, uri: uri, source: source})
		}
	}

	aggregate, err := s.aggregateEntries(ctx, resolved)
	if err != nil {
		return "", sub, err
	}
	sub.UsedBytes = aggregate.used
	sub.UploadBytes = aggregate.upload
	sub.DownloadBytes = aggregate.download
	sub.ExpiresAt = aggregate.expires
	if aggregate.allLimited {
		sub.AvailableBytes = &aggregate.available
	} else {
		sub.AvailableBytes = nil
	}
	lines = append(lines, "#used: "+humanBytes(aggregate.used))
	if aggregate.allLimited {
		lines = append(lines, "#available: "+humanBytes(aggregate.available))
	} else {
		lines = append(lines, "#available: unlimited")
	}
	lines = append(lines, "")
	for _, item := range resolved {
		lines = append(lines, item.uri)
		appendEntryMetadata(&lines, item.entry, item.source)
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n", sub, nil
}

type bundleMirror struct {
	Type      string `json:"t"`
	URL       string `json:"u"`
	Encrypted bool   `json:"e"`
	Algorithm string `json:"a"`
}

type clientBundle struct {
	Type                    string         `json:"type"`
	Version                 int            `json:"v"`
	Name                    string         `json:"n"`
	Slug                    string         `json:"s"`
	URL                     string         `json:"u"`
	Mirrors                 []bundleMirror `json:"m"`
	MirrorKey               string         `json:"mk"`
	UpdateWhenConnectedOnly bool           `json:"uc"`
	Deduplication           bool           `json:"d"`
}

// Bundle returns the compact OLCRTC Client subscription QR JSON.
func (s *Service) Bundle(ctx context.Context, slug string) ([]byte, error) {
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !sub.Enabled {
		return nil, ErrDisabled
	}
	key := ""
	mirrors := make([]bundleMirror, 0, 1)
	if sub.MirrorEnabled && sub.MirrorPublicURL != "" {
		encryptedKey, err := s.store.SubscriptionMirrorKey(ctx, sub.ID)
		if err != nil {
			return nil, err
		}
		plainKey, err := s.secrets.Decrypt(encryptedKey)
		if err != nil {
			return nil, err
		}
		key = plainKey
		mirrors = append(mirrors, bundleMirror{Type: "yandex_disk", URL: sub.MirrorPublicURL, Encrypted: true, Algorithm: "AES-256-GCM"})
	}
	s.mu.RLock()
	baseURL := s.baseURL
	s.mu.RUnlock()
	bundle := clientBundle{Type: "olcrtc-sub", Version: 2, Name: sub.Name, Slug: sub.Slug, URL: baseURL + "/sub/" + sub.Slug, Mirrors: mirrors, MirrorKey: key, UpdateWhenConnectedOnly: false, Deduplication: true}
	return json.Marshal(bundle)
}

// SyncMirror encrypts and uploads the OLCRTC Client subscription feed.
func (s *Service) SyncMirror(ctx context.Context, slug string) (string, error) {
	s.mirrorMu.Lock()
	defer s.mirrorMu.Unlock()
	enabled, err := s.store.SettingOrDefault(ctx, "yandex_enabled", "false")
	if err != nil || enabled != "true" {
		return "", errors.New("yandex mirror is disabled globally")
	}
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return "", err
	}
	if !sub.MirrorEnabled {
		return "", errors.New("mirror is disabled for this subscription")
	}
	feed, _, err := s.Standard(ctx, slug)
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
	payload, err := mirror.Encrypt(key, []byte(feed))
	if err != nil {
		return "", err
	}
	client, err := s.mirrorClient(ctx)
	if err != nil {
		return "", err
	}
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

// Delete removes a remote mirror first and only then deletes local state.
func (s *Service) Delete(ctx context.Context, slug string) error {
	s.mirrorMu.Lock()
	defer s.mirrorMu.Unlock()
	sub, err := s.store.Subscription(ctx, slug)
	if err != nil {
		return err
	}
	mirrorMayExist := sub.MirrorEnabled || sub.MirrorPublicURL != "" || (sub.MirrorStatus != "" && sub.MirrorStatus != "disabled")
	if mirrorMayExist {
		client, err := s.mirrorClient(ctx)
		if err != nil {
			return fmt.Errorf("mirror cleanup unavailable: %w", err)
		}
		if err := client.Delete(ctx, slug); err != nil {
			return fmt.Errorf("mirror cleanup failed: %w", err)
		}
	}
	return s.store.DeleteSubscription(ctx, slug)
}

func (s *Service) mirrorClient(ctx context.Context) (*mirror.Client, error) {
	tokenEncrypted, _, err := s.store.Setting(ctx, "yandex_oauth_token")
	if err != nil {
		return nil, errors.New("yandex OAuth token is not configured")
	}
	token, err := s.secrets.Decrypt(tokenEncrypted)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("yandex OAuth token is not configured")
	}
	basePath, err := s.store.SettingOrDefault(ctx, "yandex_base_path", "/olcrtc/subscriptions")
	if err != nil {
		return nil, err
	}
	return mirror.NewClient(token, basePath), nil
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

func (s *Service) resolveEntryForFormat(ctx context.Context, entry model.SubscriptionEntry, format string) (string, *model.Instance, bool, error) {
	if format != "client" && format != "olcbox" {
		return "", nil, false, fmt.Errorf("unsupported subscription format %q", format)
	}
	if entry.SourceInstanceID == nil {
		raw := strings.TrimSpace(entry.RawURI)
		valid := (format == "client" && instance.ValidateClientURI(raw) == nil) ||
			(format == "olcbox" && instance.ValidateStandardURI(raw) == nil)
		return raw, nil, valid, nil
	}
	item, err := s.instances.Get(ctx, *entry.SourceInstanceID)
	if err != nil {
		return "", nil, false, err
	}
	if format == "client" && (!instance.ClientCompatible(item.Provider, item.Transport) || (item.Provider == "wbstream" && !item.AuthTokenSet)) {
		return "", &item, false, nil
	}
	uri, err := s.instances.URI(ctx, *entry.SourceInstanceID, format)
	if err != nil {
		return "", &item, false, err
	}
	return uri, &item, true, nil
}

type trafficAggregate struct {
	used       int64
	upload     int64
	download   int64
	available  int64
	allLimited bool
	expires    *time.Time
}

func (s *Service) aggregateEntries(ctx context.Context, entries []resolvedEntry) (trafficAggregate, error) {
	result := trafficAggregate{allLimited: true}
	for _, resolved := range entries {
		entry := resolved.entry
		if entry.SourceInstanceID != nil {
			item := resolved.source
			var err error
			if item == nil {
				var loaded model.Instance
				loaded, err = s.instances.Get(ctx, *entry.SourceInstanceID)
				item = &loaded
			}
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

// TrafficHeader returns the optional Subscription-Userinfo value.
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
