package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/instance"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/subscription"
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func (s *Server) routesSubscriptions(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/subscriptions", s.requireAuth(http.HandlerFunc(s.handleSubscriptionsList)))
	mux.Handle("POST /api/v1/subscriptions", s.requireAuth(http.HandlerFunc(s.handleSubscriptionsCreate)))
	mux.Handle("GET /api/v1/subscriptions/export", s.requireAuth(http.HandlerFunc(s.handleSubscriptionsExport)))
	mux.Handle("POST /api/v1/subscriptions/import", s.requireAuth(http.HandlerFunc(s.handleSubscriptionsImport)))
	mux.Handle("GET /api/v1/subscriptions/{slug}", s.requireAuth(http.HandlerFunc(s.handleSubscriptionGet)))
	mux.Handle("PUT /api/v1/subscriptions/{slug}", s.requireAuth(http.HandlerFunc(s.handleSubscriptionUpdate)))
	mux.Handle("DELETE /api/v1/subscriptions/{slug}", s.requireAuth(http.HandlerFunc(s.handleSubscriptionDelete)))
	mux.Handle("GET /api/v1/subscriptions/{slug}/entries", s.requireAuth(http.HandlerFunc(s.handleEntriesList)))
	mux.Handle("POST /api/v1/subscriptions/{slug}/entries", s.requireAuth(http.HandlerFunc(s.handleEntriesCreate)))
	mux.Handle("PUT /api/v1/subscriptions/{slug}/entries/{id}", s.requireAuth(http.HandlerFunc(s.handleEntryUpdate)))
	mux.Handle("DELETE /api/v1/subscriptions/{slug}/entries/{id}", s.requireAuth(http.HandlerFunc(s.handleEntryDelete)))
	mux.Handle("POST /api/v1/subscriptions/{slug}/reorder", s.requireAuth(http.HandlerFunc(s.handleEntriesReorder)))
	mux.Handle("GET /api/v1/subscriptions/{slug}/qr", s.requireAuth(http.HandlerFunc(s.handleSubscriptionQR)))
	mux.Handle("GET /api/v1/subscriptions/{slug}/payload", s.requireAuth(http.HandlerFunc(s.handleSubscriptionPayload)))
	mux.Handle("POST /api/v1/subscriptions/{slug}/mirror/sync", s.requireAuth(http.HandlerFunc(s.handleMirrorSync)))
	mux.Handle("GET /api/v1/subscriptions/{slug}/mirror", s.requireAuth(http.HandlerFunc(s.handleMirrorStatus)))
}

func (s *Server) handleSubscriptionsList(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Subscriptions(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "subscriptions_read_failed", "Не удалось прочитать подписки")
		return
	}
	for i := range items {
		_, resolved, renderErr := s.subscriptions.Standard(r.Context(), items[i].Slug)
		if renderErr == nil {
			items[i].UsedBytes = resolved.UsedBytes
			items[i].AvailableBytes = resolved.AvailableBytes
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleSubscriptionsCreate(w http.ResponseWriter, r *http.Request) {
	var input model.Subscription
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте поля подписки")
		return
	}
	if input.Slug == "" {
		input.Slug, _ = security.RandomToken(16)
	}
	if err := validateSubscription(input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_subscription", err.Error())
		return
	}
	if input.RefreshInterval == "" {
		input.RefreshInterval = "10m"
	}
	if !input.Enabled && input.CreatedAt.IsZero() {
		input.Enabled = true
	}
	input.MirrorStatus = "disabled"
	_, encryptedKey, err := s.subscriptions.GenerateMirrorKey()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "mirror_key_failed", "Не удалось создать ключ mirror")
		return
	}
	item, err := s.store.CreateSubscription(r.Context(), input, encryptedKey)
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, r, http.StatusConflict, "slug_exists", "Такой slug уже используется")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "subscription_create_failed", "Не удалось создать подписку")
		return
	}
	audit(s, r, "subscription.create", "subscription", item.Slug, "success", "")
	if item.MirrorEnabled {
		s.subscriptionsChanged(r.Context(), []string{item.Slug})
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleSubscriptionGet(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleSubscriptionUpdate(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	current, err := s.store.Subscription(r.Context(), slug)
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	var input model.Subscription
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте поля подписки")
		return
	}
	input.ID = current.ID
	input.Slug = slug
	input.Entries = current.Entries
	input.MirrorPublicURL = current.MirrorPublicURL
	if input.RefreshInterval == "" {
		input.RefreshInterval = current.RefreshInterval
	}
	if err := validateSubscription(input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_subscription", err.Error())
		return
	}
	if !input.MirrorEnabled {
		input.MirrorStatus = "disabled"
	} else if input.MirrorStatus == "" {
		input.MirrorStatus = current.MirrorStatus
	}
	item, err := s.store.UpdateSubscription(r.Context(), input)
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	audit(s, r, "subscription.update", "subscription", slug, "success", "")
	s.subscriptionsChanged(r.Context(), []string{slug})
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := s.subscriptions.Delete(r.Context(), slug); err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	audit(s, r, "subscription.delete", "subscription", slug, "success", "remote mirror removed before local state")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEntriesList(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item.Entries)
}

func (s *Server) handleEntriesCreate(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	var input model.SubscriptionEntry
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте поля entry")
		return
	}
	input.SubscriptionID = sub.ID
	if !input.Enabled && input.CreatedAt.IsZero() {
		input.Enabled = true
	}
	if input.SortOrder == 0 {
		input.SortOrder = len(sub.Entries)
	}
	if err := s.validateEntry(r, input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_entry", err.Error())
		return
	}
	item, err := s.store.AddSubscriptionEntry(r.Context(), input)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "entry_create_failed", "Не удалось добавить entry")
		return
	}
	s.subscriptionsChanged(r.Context(), []string{sub.Slug})
	audit(s, r, "subscription.entry_create", "subscription", sub.Slug, "success", "entry_id="+strconv.FormatInt(item.ID, 10))
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleEntryUpdate(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID entry")
		return
	}
	current, err := s.store.SubscriptionEntry(r.Context(), id)
	if err != nil || current.SubscriptionID != sub.ID {
		s.subscriptionError(w, r, sql.ErrNoRows)
		return
	}
	var input model.SubscriptionEntry
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте поля entry")
		return
	}
	input.ID = id
	input.SubscriptionID = sub.ID
	if err := s.validateEntry(r, input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_entry", err.Error())
		return
	}
	item, err := s.store.UpdateSubscriptionEntry(r.Context(), input)
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	s.subscriptionsChanged(r.Context(), []string{sub.Slug})
	audit(s, r, "subscription.entry_update", "subscription", sub.Slug, "success", "entry_id="+strconv.FormatInt(item.ID, 10))
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleEntryDelete(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID entry")
		return
	}
	if err := s.store.DeleteSubscriptionEntry(r.Context(), sub.ID, id); err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	s.subscriptionsChanged(r.Context(), []string{sub.Slug})
	audit(s, r, "subscription.entry_delete", "subscription", sub.Slug, "success", "entry_id="+strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEntriesReorder(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	var input struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(w, r, &input); err != nil || len(input.IDs) != len(sub.Entries) {
		writeError(w, r, http.StatusBadRequest, "invalid_order", "Передайте полный список entry ID")
		return
	}
	want := make([]int64, 0, len(sub.Entries))
	for _, entry := range sub.Entries {
		want = append(want, entry.ID)
	}
	got := append([]int64(nil), input.IDs...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	for i := range want {
		if want[i] != got[i] {
			writeError(w, r, http.StatusBadRequest, "invalid_order", "Список содержит неизвестные или повторяющиеся ID")
			return
		}
	}
	if err := s.store.ReorderSubscriptionEntries(r.Context(), sub.ID, input.IDs); err != nil {
		writeError(w, r, http.StatusInternalServerError, "reorder_failed", "Не удалось изменить порядок")
		return
	}
	s.subscriptionsChanged(r.Context(), []string{sub.Slug})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSubscriptionQR(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	format := r.URL.Query().Get("format")
	if format != "" && format != "client" {
		writeError(w, r, http.StatusBadRequest, "invalid_format", "Неизвестный формат QR")
		return
	}
	b, err := s.subscriptions.Bundle(r.Context(), slug)
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	writeQR(w, r, string(b), "olcrtc-subscription-"+slug+"-client.png")
}

func (s *Server) handleSubscriptionPayload(w http.ResponseWriter, r *http.Request) {
	b, err := s.subscriptions.Bundle(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"payload": string(b)})
}

func (s *Server) handleMirrorSync(w http.ResponseWriter, r *http.Request) {
	url, err := s.subscriptions.SyncMirror(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "mirror_sync_failed", err.Error())
		return
	}
	audit(s, r, "mirror.sync", "subscription", r.PathValue("slug"), "success", "")
	writeJSON(w, http.StatusOK, map[string]string{"public_url": url, "status": "synced"})
}

func (s *Server) handleMirrorStatus(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.subscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": item.MirrorEnabled, "status": item.MirrorStatus, "public_url": item.MirrorPublicURL, "updated_at": item.UpdatedAt})
}

func (s *Server) handleSubscriptionsExport(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Subscriptions(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "export_failed", "Не удалось экспортировать подписки")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="olcrtc-subscriptions.json"`)
	writeJSON(w, http.StatusOK, map[string]any{"version": 1, "exported_at": time.Now(), "subscriptions": items})
}

func (s *Server) handleSubscriptionsImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version       int                  `json:"version"`
		Subscriptions []model.Subscription `json:"subscriptions"`
	}
	if err := decodeJSON(w, r, &input); err != nil || input.Version != 1 || len(input.Subscriptions) > 100 {
		writeError(w, r, http.StatusBadRequest, "invalid_import", "Некорректный файл импорта")
		return
	}
	created := 0
	for _, sub := range input.Subscriptions {
		if err := validateSubscription(sub); err != nil {
			continue
		}
		_, encryptedKey, err := s.subscriptions.GenerateMirrorKey()
		if err != nil {
			continue
		}
		entries := sub.Entries
		sub.Entries = nil
		sub.ID = 0
		sub.MirrorPublicURL = ""
		sub.MirrorStatus = "disabled"
		item, err := s.store.CreateSubscription(r.Context(), sub, encryptedKey)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			entry.ID = 0
			entry.SubscriptionID = item.ID
			if s.validateEntry(r, entry) == nil {
				_, _ = s.store.AddSubscriptionEntry(r.Context(), entry)
			}
		}
		if item.MirrorEnabled {
			s.subscriptionsChanged(r.Context(), []string{item.Slug})
		}
		created++
	}
	audit(s, r, "subscription.import", "subscription", "", "success", "created="+strconv.Itoa(created))
	writeJSON(w, http.StatusOK, map[string]int{"created": created})
}

func (s *Server) handlePublicStandardSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.allowPublic(w, r) {
		return
	}
	body, sub, err := s.subscriptions.Standard(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.publicSubscriptionError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Subscription-Userinfo", subscription.TrafficHeader(sub))
	_, _ = w.Write([]byte(body))
}

func (s *Server) handlePublicSubscriptionOpen(w http.ResponseWriter, r *http.Request) {
	if !s.allowPublic(w, r) {
		return
	}
	sub, err := s.store.Subscription(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.publicSubscriptionError(w, r, err)
		return
	}
	if !sub.Enabled {
		s.publicSubscriptionError(w, r, subscription.ErrDisabled)
		return
	}
	source := strings.TrimSuffix(publicBaseURL(s.cfg), "/") + "/sub/" + sub.Slug
	target := url.URL{Scheme: "olcrtc", Host: "subscription", RawQuery: url.Values{"url": {source}, "name": {sub.Name}}.Encode()}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) publicSubscriptionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, subscription.ErrDisabled) {
		http.Error(w, "Gone", http.StatusGone)
		return
	}
	if store.IsNotFound(err) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "Subscription unavailable", http.StatusServiceUnavailable)
}

func (s *Server) validateEntry(r *http.Request, entry model.SubscriptionEntry) error {
	linked := entry.SourceInstanceID != nil
	manual := strings.TrimSpace(entry.RawURI) != ""
	if linked == manual {
		return errors.New("entry должен быть linked instance или manual URI")
	}
	if linked {
		item, err := s.instances.Get(r.Context(), *entry.SourceInstanceID)
		if err != nil {
			return errors.New("linked instance не найден")
		}
		if !instance.ClientCompatible(item.Provider, item.Transport) {
			return fmt.Errorf("OLCRTC Client не поддерживает %s + %s", item.Provider, item.Transport)
		}
		if item.Provider == "wbstream" && !item.AuthTokenSet {
			return errors.New("для WB linked instance сначала получите auth token")
		}
	}
	if manual {
		if err := instance.ValidateClientURI(entry.RawURI); err != nil {
			return err
		}
	}
	for _, value := range []string{entry.Name, entry.Color, entry.Icon, entry.IP, entry.Comment} {
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("metadata должна быть однострочной")
		}
	}
	if entry.Color != "" && !colorPattern.MatchString(entry.Color) {
		return errors.New("цвет должен иметь формат #RRGGBB")
	}
	return nil
}

func validateSubscription(item model.Subscription) error {
	if strings.TrimSpace(item.Name) == "" || strings.ContainsAny(item.Name, "\r\n") {
		return errors.New("имя подписки обязательно")
	}
	if !slugPattern.MatchString(item.Slug) {
		return errors.New("slug должен содержать 16-128 букв, цифр, '_' или '-'")
	}
	if item.RefreshInterval != "" {
		if _, err := parseRefresh(item.RefreshInterval); err != nil {
			return errors.New("refresh должен быть duration, например 10m или 6h")
		}
	}
	if item.Color != "" && !colorPattern.MatchString(item.Color) {
		return errors.New("цвет должен иметь формат #RRGGBB")
	}
	return nil
}

func parseRefresh(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days < 1 {
			return 0, errors.New("invalid days")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(value)
}

func (s *Server) subscriptionError(w http.ResponseWriter, r *http.Request, err error) {
	if store.IsNotFound(err) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "subscription_not_found", "Подписка или entry не найдены")
		return
	}
	writeError(w, r, http.StatusBadRequest, "subscription_operation_failed", err.Error())
}

func publicBaseURL(cfg config.Config) string {
	host := cfg.PublicIP
	if host == "" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host + ":" + strconv.Itoa(cfg.PublicPort)
}
