package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/security"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	if allowed, retry := s.limiter.allow(ip); !allowed {
		w.Header().Set("Retry-After", "1")
		writeError(w, r, http.StatusTooManyRequests, "login_rate_limited", "Слишком много попыток входа. Повторите позже.")
		_ = retry
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Некорректный запрос")
		return
	}
	admin, err := s.store.AdminByUsername(r.Context(), strings.TrimSpace(input.Username))
	valid := err == nil && security.VerifyPassword(admin.PasswordHash, input.Password)
	if !valid {
		delay := s.limiter.fail(ip)
		time.Sleep(delay)
		audit(s, r, "login", "session", "", "failed", "invalid credentials")
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Неверный логин или пароль")
		return
	}
	s.limiter.success(ip)
	token, err := security.RandomToken(32)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "Не удалось создать сессию")
		return
	}
	csrf, err := security.RandomToken(24)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "Не удалось создать сессию")
		return
	}
	now := time.Now()
	session := model.Session{IDHash: security.HashToken(token), AdminID: admin.ID, CSRFHash: security.HashToken(csrf), CreatedAt: now, ExpiresAt: now.Add(12 * time.Hour), LastSeenAt: now, IP: ip, UserAgent: truncate(r.UserAgent(), 512)}
	if err := s.store.CreateSession(r.Context(), session); err != nil {
		writeError(w, r, http.StatusInternalServerError, "session_create_failed", "Не удалось создать сессию")
		return
	}
	s.setSessionCookies(w, token, csrf, session.ExpiresAt)
	audit(s, r, "login", "session", "", "success", "")
	writeJSON(w, http.StatusOK, map[string]any{"username": admin.Username, "csrf_token": csrf, "expires_at": session.ExpiresAt})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if session, ok := r.Context().Value(sessionKey).(model.Session); ok {
		_ = s.store.DeleteSession(r.Context(), session.IDHash)
	}
	s.clearSessionCookies(w)
	audit(s, r, "logout", "session", "", "success", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	admin, err := s.store.Admin(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "admin_read_failed", "Не удалось прочитать профиль")
		return
	}
	csrf, _ := r.Cookie(s.cfg.CookieName + "_csrf")
	token := ""
	if csrf != nil {
		token = csrf.Value
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": admin.Username, "csrf_token": token})
}

func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Некорректные данные")
		return
	}
	admin, err := s.store.Admin(r.Context())
	if err != nil || !security.VerifyPassword(admin.PasswordHash, input.CurrentPassword) {
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Текущий пароль неверен")
		return
	}
	username := strings.TrimSpace(input.Username)
	if username == "" {
		username = admin.Username
	}
	if len(username) < 3 || len(username) > 64 || strings.ContainsAny(username, "\r\n\t ") {
		writeError(w, r, http.StatusBadRequest, "invalid_username", "Логин должен содержать 3-64 символа без пробелов")
		return
	}
	password := input.NewPassword
	if password == "" {
		password = input.CurrentPassword
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_password", "Новый пароль должен содержать не менее 12 символов")
		return
	}
	if err := s.store.UpdateAdminCredentials(r.Context(), username, hash); err != nil {
		writeError(w, r, http.StatusInternalServerError, "credentials_update_failed", "Не удалось изменить учётные данные")
		return
	}
	s.clearSessionCookies(w)
	audit(s, r, "credentials.update", "admin", "1", "success", "all sessions revoked")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSessions(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSessions(r.Context()); err != nil {
		writeError(w, r, http.StatusInternalServerError, "sessions_revoke_failed", "Не удалось завершить сессии")
		return
	}
	s.clearSessionCookies(w)
	audit(s, r, "sessions.revoke", "admin", "1", "success", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSessionCookies(w http.ResponseWriter, token, csrf string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: s.cfg.CookieName, Value: token, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: s.cfg.CookieName + "_csrf", Value: csrf, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), Secure: true, HttpOnly: false, SameSite: http.SameSiteStrictMode})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{s.cfg.CookieName, s.cfg.CookieName + "_csrf"} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: name == s.cfg.CookieName, SameSite: http.SameSiteStrictMode})
	}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
