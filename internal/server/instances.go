package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/instance"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/store"
	"rsc.io/qr"
)

func (s *Server) routesInstances(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/instances", s.requireAuth(http.HandlerFunc(s.handleInstancesList)))
	mux.Handle("POST /api/v1/instances", s.requireAuth(http.HandlerFunc(s.handleInstancesCreate)))
	mux.Handle("GET /api/v1/instances/{id}", s.requireAuth(http.HandlerFunc(s.handleInstanceGet)))
	mux.Handle("PUT /api/v1/instances/{id}", s.requireAuth(http.HandlerFunc(s.handleInstanceUpdate)))
	mux.Handle("DELETE /api/v1/instances/{id}", s.requireAuth(http.HandlerFunc(s.handleInstanceDelete)))
	for _, action := range []string{"start", "stop", "restart", "duplicate", "rotate-key", "change-room", "reset-traffic", "diagnostics"} {
		mux.Handle("POST /api/v1/instances/{id}/"+action, s.requireAuth(http.HandlerFunc(s.handleInstanceAction)))
	}
	mux.Handle("GET /api/v1/instances/{id}/uri", s.requireAuth(http.HandlerFunc(s.handleInstanceURI)))
	mux.Handle("GET /api/v1/instances/{id}/qr", s.requireAuth(http.HandlerFunc(s.handleInstanceQR)))
	mux.Handle("GET /api/v1/instances/{id}/logs", s.requireAuth(http.HandlerFunc(s.handleInstanceLogs)))
}

func (s *Server) handleInstancesList(w http.ResponseWriter, r *http.Request) {
	items, err := s.instances.List(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "instances_read_failed", "Не удалось прочитать инстансы")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "summary": instanceSummary(items)})
}

type instanceInput struct {
	model.Instance
	ClearAuthToken     bool `json:"clear_auth_token"`
	ClearOutboundProxy bool `json:"clear_outbound_proxy"`
}

func (s *Server) handleInstancesCreate(w http.ResponseWriter, r *http.Request) {
	var input instanceInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте поля инстанса")
		return
	}
	item, err := s.instances.Create(r.Context(), input.Instance)
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	audit(s, r, "instance.create", "instance", strconv.FormatInt(item.ID, 10), "success", "provider="+item.Provider+", transport="+item.Transport)
	writeJSON(w, http.StatusCreated, map[string]any{"instance": item, "warning": instance.CompatibilityWarning(item.Provider, item.Transport)})
}

func (s *Server) handleInstanceGet(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID")
		return
	}
	item, err := s.instances.Get(r.Context(), id)
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": item, "warning": instance.CompatibilityWarning(item.Provider, item.Transport)})
}

func (s *Server) handleInstanceUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID")
		return
	}
	slugs, _ := s.store.SubscriptionSlugsForInstance(r.Context(), id)
	var input instanceInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте поля инстанса")
		return
	}
	input.ID = id
	item, err := s.instances.Update(r.Context(), input.Instance, input.ClearAuthToken, input.ClearOutboundProxy)
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	audit(s, r, "instance.update", "instance", strconv.FormatInt(id, 10), "success", "configuration applied atomically")
	s.subscriptionsChanged(r.Context(), slugs)
	writeJSON(w, http.StatusOK, map[string]any{"instance": item, "warning": instance.CompatibilityWarning(item.Provider, item.Transport)})
}

func (s *Server) handleInstanceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID")
		return
	}
	item, err := s.instances.Get(r.Context(), id)
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	var input struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decodeJSON(w, r, &input); err != nil || input.ConfirmName != item.Name {
		writeError(w, r, http.StatusBadRequest, "confirmation_failed", "Для удаления введите точное имя инстанса")
		return
	}
	slugs, _ := s.store.SubscriptionSlugsForInstance(r.Context(), id)
	if err := s.instances.Delete(r.Context(), id); err != nil {
		s.instanceError(w, r, err)
		return
	}
	audit(s, r, "instance.delete", "instance", strconv.FormatInt(id, 10), "success", "linked entries removed")
	s.subscriptionsChanged(r.Context(), slugs)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInstanceAction(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID")
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/instances/"+strconv.FormatInt(id, 10)+"/")
	slugs, _ := s.store.SubscriptionSlugsForInstance(r.Context(), id)
	switch action {
	case "start":
		err = s.instances.Start(r.Context(), id)
	case "stop":
		err = s.instances.Stop(r.Context(), id)
	case "restart":
		err = s.instances.Restart(r.Context(), id)
	case "rotate-key":
		err = s.instances.RotateKey(r.Context(), id)
	case "reset-traffic":
		err = s.store.ResetTraffic(r.Context(), id, time.Now())
	case "duplicate":
		var duplicate model.Instance
		duplicate, err = s.instances.Duplicate(r.Context(), id)
		if err == nil {
			audit(s, r, "instance.duplicate", "instance", strconv.FormatInt(id, 10), "success", "new_id="+strconv.FormatInt(duplicate.ID, 10))
			writeJSON(w, http.StatusCreated, duplicate)
			return
		}
	case "change-room":
		var input struct {
			RoomID string `json:"room_id"`
		}
		if decodeErr := decodeJSON(w, r, &input); decodeErr != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "Укажите Room ID")
			return
		}
		var changed model.Instance
		changed, err = s.instances.ChangeRoom(r.Context(), id, input.RoomID)
		if err == nil {
			audit(s, r, "instance.change_room", "instance", strconv.FormatInt(id, 10), "success", "")
			s.subscriptionsChanged(r.Context(), slugs)
			writeJSON(w, http.StatusOK, changed)
			return
		}
	case "diagnostics":
		item, getErr := s.instances.Get(r.Context(), id)
		if getErr != nil {
			err = getErr
			break
		}
		result, diagnosticErr := diagnoseProvider(r.Context(), item)
		if diagnosticErr != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": diagnosticErr.Error(), "warning": instance.CompatibilityWarning(item.Provider, item.Transport)})
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	default:
		err = errors.New("unsupported action")
	}
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	audit(s, r, "instance."+strings.ReplaceAll(action, "-", "_"), "instance", strconv.FormatInt(id, 10), "success", "")
	if action == "rotate-key" {
		s.subscriptionsChanged(r.Context(), slugs)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) subscriptionsChanged(ctx context.Context, slugs []string) {
	if len(slugs) == 0 {
		return
	}
	_ = s.store.TouchSubscriptions(ctx, slugs)
	background := context.WithoutCancel(ctx)
	for _, slug := range slugs {
		sub, err := s.store.Subscription(background, slug)
		if err != nil || !sub.MirrorEnabled {
			continue
		}
		slug := slug
		go func() { _, _ = s.subscriptions.SyncMirror(background, slug) }()
	}
}

func (s *Server) handleInstanceURI(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "standard"
	}
	value, err := s.instances.URI(r.Context(), id, format)
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"format": format, "uri": value})
}

func (s *Server) handleInstanceQR(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "standard"
	}
	payload, err := s.instances.URI(r.Context(), id, format)
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	writeQR(w, r, payload, "olcrtc-instance-"+strconv.FormatInt(id, 10)+"-"+format+".png")
}

func (s *Server) handleInstanceLogs(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_id", "Некорректный ID")
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	text, err := s.instances.Logs(r.Context(), id, lines)
	if err != nil {
		s.instanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

func (s *Server) instanceError(w http.ResponseWriter, r *http.Request, err error) {
	if instance.IsNotFound(err) || store.IsNotFound(err) {
		writeError(w, r, http.StatusNotFound, "instance_not_found", "Инстанс не найден")
		return
	}
	if store.IsUniqueViolation(err) {
		writeError(w, r, http.StatusConflict, "instance_name_exists", "Инстанс с таким именем уже существует")
		return
	}
	writeError(w, r, http.StatusBadRequest, "instance_operation_failed", err.Error())
}

func instanceSummary(items []model.Instance) map[string]any {
	result := map[string]any{"count": len(items), "running": 0, "failed": 0, "upload_bytes": int64(0), "download_bytes": int64(0), "total_bytes": int64(0), "active_sessions": 0}
	for _, item := range items {
		if item.Status == "running" {
			result["running"] = result["running"].(int) + 1
		}
		if item.Status == "failed" {
			result["failed"] = result["failed"].(int) + 1
		}
		result["upload_bytes"] = result["upload_bytes"].(int64) + item.UploadBytes
		result["download_bytes"] = result["download_bytes"].(int64) + item.DownloadBytes
		result["total_bytes"] = result["total_bytes"].(int64) + item.TotalBytes
	}
	return result
}

func writeQR(w http.ResponseWriter, r *http.Request, payload, filename string) {
	code, err := qr.Encode(payload, qr.M)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "qr_too_large", "Данные не помещаются в QR-код")
		return
	}
	code.Scale = 8
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(code.PNG())
}

func diagnoseProvider(ctx context.Context, item model.Instance) (map[string]any, error) {
	result := map[string]any{"ok": true, "provider": item.Provider, "transport": item.Transport, "warning": instance.CompatibilityWarning(item.Provider, item.Transport), "checked_at": time.Now()}
	if item.Provider != "jitsi" {
		result["message"] = "Конфигурация прошла локальную проверку; удалённая проверка этого provider не поддерживается."
		return result, nil
	}
	room := item.RoomID
	if !strings.Contains(room, "://") {
		room = "https://" + room
	}
	u, err := url.Parse(room)
	if err != nil {
		return nil, err
	}
	if err := ensurePublicHost(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	target := u.Scheme + "://" + u.Host + "/config.js"
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: safeDialer(ctx, u.Hostname())}
	client := &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return ensurePublicHost(req.Context(), req.URL.Hostname())
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Jitsi недоступен: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	result["status_code"] = resp.StatusCode
	result["endpoint"] = target
	result["ok"] = resp.StatusCode >= 200 && resp.StatusCode < 400
	return result, nil
}

func ensurePublicHost(ctx context.Context, host string) error {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return errors.New("не удалось разрешить адрес provider")
	}
	for _, address := range addresses {
		if isPrivateIP(address.IP) {
			return errors.New("provider указывает в запрещённую private/loopback сеть")
		}
	}
	return nil
}

func safeDialer(ctx context.Context, expectedHost string) func(context.Context, string, string) (net.Conn, error) {
	return func(callCtx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(host, expectedHost) {
			if err := ensurePublicHost(callCtx, host); err != nil {
				return nil, err
			}
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(callCtx, host)
		if err != nil {
			return nil, err
		}
		for _, resolved := range addresses {
			if !isPrivateIP(resolved.IP) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(callCtx, network, net.JoinHostPort(resolved.IP.String(), port))
			}
		}
		return nil, errors.New("provider has no public address")
	}
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
