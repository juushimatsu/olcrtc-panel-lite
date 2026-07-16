// Package server implements the HTTPS panel API and embedded frontend.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/backup"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/instance"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/subscription"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/web"
)

type contextKey string

const (
	requestIDKey contextKey = "request-id"
	sessionKey   contextKey = "session"
)

// Server owns API dependencies but never executes arbitrary user commands.
type Server struct {
	cfg           config.Config
	store         *store.Store
	instances     *instance.Manager
	subscriptions *subscription.Service
	secrets       *security.Secrets
	backups       *backup.Manager
	startedAt     time.Time
	limiter       *loginLimiter
	publicLimiter *windowLimiter
	networkSpeed  *speedSampler
	operations    *operationTracker
	logger        *slog.Logger
}

// New creates the complete API and SPA handler.
func New(cfg config.Config, st *store.Store, instances *instance.Manager, subscriptions *subscription.Service, secrets *security.Secrets, logger *slog.Logger) http.Handler {
	server := &Server{cfg: cfg, store: st, instances: instances, subscriptions: subscriptions, secrets: secrets, backups: backup.NewManager(st.DB(), cfg.InstancesDir, cfg.BackupDir), startedAt: time.Now(), limiter: newLoginLimiter(), publicLimiter: newWindowLimiter(120, time.Minute), networkSpeed: &speedSampler{}, operations: newOperationTracker(), logger: logger}
	mux := http.NewServeMux()
	server.routes(mux)
	return server.securityHeaders(server.requestContext(mux))
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /api/v1/auth/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("PUT /api/v1/auth/credentials", s.requireAuth(http.HandlerFunc(s.handleCredentials)))
	mux.Handle("DELETE /api/v1/auth/sessions", s.requireAuth(http.HandlerFunc(s.handleDeleteSessions)))

	mux.Handle("GET /api/v1/system/status", s.requireAuth(http.HandlerFunc(s.handleSystemStatus)))
	mux.Handle("GET /api/v1/system/metrics", s.requireAuth(http.HandlerFunc(s.handleSystemMetrics)))
	mux.Handle("GET /api/v1/system/certificate", s.requireAuth(http.HandlerFunc(s.handleCertificate)))
	mux.Handle("POST /api/v1/system/certificate/regenerate", s.requireAuth(http.HandlerFunc(s.handleRegenerateCertificate)))
	mux.Handle("GET /api/v1/system/logs", s.requireAuth(http.HandlerFunc(s.handleSystemLogs)))
	mux.Handle("GET /api/v1/system/audit", s.requireAuth(http.HandlerFunc(s.handleAudit)))
	mux.Handle("POST /api/v1/system/backup", s.requireAuth(http.HandlerFunc(s.handleBackup)))
	mux.Handle("GET /api/v1/system/backup/{id}", s.requireAuth(http.HandlerFunc(s.handleBackupDownload)))

	s.routesInstances(mux)
	s.routesSubscriptions(mux)
	s.routesSettings(mux)

	mux.HandleFunc("GET /sub/{slug}", s.handlePublicStandardSubscription)
	mux.HandleFunc("GET /sub/{slug}/exclave", s.handlePublicExclaveSubscription)
	mux.HandleFunc("GET /ca.crt", s.handleCA)
	proxyTarget, _ := url.Parse("http://127.0.0.1:6080")
	novnc := httputil.NewSingleHostReverseProxy(proxyTarget)
	mux.Handle("/wb/novnc/", s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/wb/novnc")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		novnc.ServeHTTP(w, r)
	})))
	mux.Handle("/", s.frontend())
}

func (s *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idBytes := make([]byte, 8)
		_, _ = rand.Read(idBytes)
		id := hex.EncodeToString(idBytes)
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		frameAncestors := "'none'"
		scriptSource := "'self'"
		connectSource := "'self'"
		if strings.HasPrefix(r.URL.Path, "/wb/novnc/") {
			frameAncestors = "'self'"
			scriptSource = "'self' 'unsafe-eval'"
			connectSource = "'self' ws: wss:"
		}
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src "+scriptSource+"; connect-src "+connectSource+"; frame-ancestors "+frameAncestors+"; base-uri 'none'; form-action 'self'")
		if s.cfg.HSTS {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.authenticate(r)
		if !ok {
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "Требуется вход в панель")
			return
		}
		if isMutating(r.Method) {
			csrfCookie, err := r.Cookie(s.cfg.CookieName + "_csrf")
			if err != nil || r.Header.Get("X-CSRF-Token") == "" || r.Header.Get("X-CSRF-Token") != csrfCookie.Value || !security.EqualToken(csrfCookie.Value, session.CSRFHash) {
				writeError(w, r, http.StatusForbidden, "csrf_failed", "Проверка CSRF не пройдена")
				return
			}
		}
		ctx := context.WithValue(r.Context(), sessionKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authenticate(r *http.Request) (model.Session, bool) {
	cookie, err := r.Cookie(s.cfg.CookieName)
	if err != nil || cookie.Value == "" {
		return model.Session{}, false
	}
	session, err := s.store.Session(r.Context(), security.HashToken(cookie.Value))
	if err != nil {
		return model.Session{}, false
	}
	now := time.Now()
	maxExpires := session.CreatedAt.Add(7 * 24 * time.Hour)
	expires := session.ExpiresAt
	if expires.Sub(now) < time.Hour {
		expires = now.Add(12 * time.Hour)
		if expires.After(maxExpires) {
			expires = maxExpires
		}
	}
	_ = s.store.TouchSession(r.Context(), session.IDHash, now, expires)
	return session, true
}

func (s *Server) frontend() http.Handler {
	root, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" || !strings.Contains(filepath.Base(r.URL.Path), ".") {
			b, err := fs.ReadFile(root, "index.html")
			if err != nil {
				http.Error(w, "frontend unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(b)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, output any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": requestID(r)}})
}

func requestID(r *http.Request) string {
	value, _ := r.Context().Value(requestIDKey).(string)
	return value
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func isMutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func parseID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid numeric ID")
	}
	return id, nil
}

func audit(s *Server, r *http.Request, action, objectType, objectID, result, details string) {
	_ = s.store.AddAudit(r.Context(), model.AuditEvent{Action: action, ObjectType: objectType, ObjectID: objectID, Result: result, ActorIP: remoteIP(r), DetailsRedacted: details})
}

type loginAttempt struct {
	failures     int
	first        time.Time
	blockedUntil time.Time
}
type loginLimiter struct {
	mu     sync.Mutex
	values map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{values: make(map[string]loginAttempt)} }

func (l *loginLimiter) allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	item := l.values[ip]
	if now.Sub(item.first) > 10*time.Minute {
		delete(l.values, ip)
		return true, 0
	}
	if now.Before(item.blockedUntil) {
		return false, time.Until(item.blockedUntil)
	}
	return true, 0
}

func (l *loginLimiter) fail(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	item := l.values[ip]
	if item.first.IsZero() || now.Sub(item.first) > 10*time.Minute {
		item = loginAttempt{first: now}
	}
	item.failures++
	delay := time.Duration(1<<min(item.failures-1, 4)) * 100 * time.Millisecond
	if item.failures >= 5 {
		delay = min(delay, 10*time.Second)
		item.blockedUntil = item.first.Add(10 * time.Minute)
	}
	l.values[ip] = item
	return delay
}

func (l *loginLimiter) success(ip string) { l.mu.Lock(); delete(l.values, ip); l.mu.Unlock() }

type windowValue struct {
	count   int
	started time.Time
}

type windowLimiter struct {
	mu      sync.Mutex
	values  map[string]windowValue
	maximum int
	window  time.Duration
}

func newWindowLimiter(maximum int, window time.Duration) *windowLimiter {
	return &windowLimiter{values: make(map[string]windowValue), maximum: maximum, window: window}
}

func (l *windowLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	item := l.values[key]
	if item.started.IsZero() || now.Sub(item.started) >= l.window {
		item = windowValue{started: now}
	}
	item.count++
	l.values[key] = item
	return item.count <= l.maximum
}

func (s *Server) allowPublic(w http.ResponseWriter, r *http.Request) bool {
	if s.publicLimiter.allow(remoteIP(r)) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
	return false
}

type speedSampler struct {
	mu      sync.Mutex
	at      time.Time
	ingress int64
	egress  int64
}

func (s *speedSampler) sample(ingress, egress int64) (float64, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.at.IsZero() || ingress < s.ingress || egress < s.egress {
		s.at, s.ingress, s.egress = now, ingress, egress
		return 0, 0
	}
	seconds := now.Sub(s.at).Seconds()
	if seconds <= 0 {
		return 0, 0
	}
	inRate := float64(ingress-s.ingress) / seconds
	outRate := float64(egress-s.egress) / seconds
	s.at, s.ingress, s.egress = now, ingress, egress
	return inRate, outRate
}
