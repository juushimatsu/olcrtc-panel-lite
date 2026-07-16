package server

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/backup"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/certificates"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/redact"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/sysinfo"
)

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	items, err := s.instances.List(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "instances_read_failed", "Не удалось получить состояние инстансов")
		return
	}
	counts := map[string]int{"running": 0, "stopped": 0, "failed": 0, "unknown": 0}
	var upload, download, total, ingress, egress int64
	for _, item := range items {
		counts[item.Status]++
		upload += item.UploadBytes
		download += item.DownloadBytes
		total += item.TotalBytes
		ingress += item.NetworkIngressBytes
		egress += item.NetworkEgressBytes
	}
	ingressRate, egressRate := s.networkSpeed.sample(ingress, egress)
	cert, _ := certificates.Ensure(s.cfg.TLSDir, s.cfg.PublicIP)
	writeJSON(w, http.StatusOK, map[string]any{"panel": "running", "panel_version": s.cfg.PanelVersion, "upstream_sha": s.cfg.UpstreamSHA, "panel_uptime_seconds": int64(time.Since(s.startedAt).Seconds()), "public_ip": s.cfg.PublicIP, "public_port": s.cfg.PublicPort, "instances": counts, "traffic": map[string]int64{"upload_bytes": upload, "download_bytes": download, "total_bytes": total}, "network_speed": map[string]any{"ingress_bytes_per_second": ingressRate, "egress_bytes_per_second": egressRate, "approximate": true}, "certificate_fingerprint": cert.ServerFingerprint, "wb": wbStatus(), "update_configured": s.cfg.ReleaseManifestURL != ""})
}

func (s *Server) handleSystemMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, sysinfo.Collect(s.cfg.ReleaseDir))
}

func (s *Server) handleCertificate(w http.ResponseWriter, r *http.Request) {
	info, err := certificates.Ensure(s.cfg.TLSDir, s.cfg.PublicIP)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "certificate_read_failed", "Не удалось прочитать сертификат")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleRegenerateCertificate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PublicIP string `json:"public_ip"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Некорректный IP")
		return
	}
	ip := strings.TrimSpace(input.PublicIP)
	if netIP := netParseIP(ip); netIP == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_public_ip", "Укажите корректный IP-адрес")
		return
	}
	info, err := certificates.RegenerateServer(s.cfg.TLSDir, ip)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "certificate_regenerate_failed", "Не удалось регенерировать сертификат")
		return
	}
	s.cfg.PublicIP = ip
	_ = s.store.SetSetting(r.Context(), "public_ip", ip, false)
	s.subscriptions.SetBaseURL(publicBaseURL(s.cfg))
	audit(s, r, "certificate.regenerate", "system", "tls", "success", "public IP changed")
	writeJSON(w, http.StatusOK, info)
}

func netParseIP(value string) string {
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return ""
}

func (s *Server) handleCA(w http.ResponseWriter, r *http.Request) {
	if !s.allowPublic(w, r) {
		return
	}
	info, err := certificates.Ensure(s.cfg.TLSDir, s.cfg.PublicIP)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="olcrtc-panel-ca.crt"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, info.CACertPath)
}

func (s *Server) handleSystemLogs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines < 1 || lines > 2000 {
		lines = 200
	}
	unit := r.URL.Query().Get("unit")
	var output string
	var err error
	if strings.HasPrefix(unit, "instance:") {
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(unit, "instance:"), 10, 64)
		if parseErr != nil || id < 1 {
			writeError(w, r, http.StatusBadRequest, "invalid_unit", "Некорректный unit")
			return
		}
		output, err = s.instances.Logs(r.Context(), id, lines)
	} else {
		allowed := map[string]string{"panel": "olcrtc-panel.service", "wb": "olcrtc-wb-session.service", "update": "olcrtc-panel-update.service"}
		name, ok := allowed[unit]
		if !ok {
			name = allowed["panel"]
		}
		if runtime.GOOS != "linux" {
			output = "Журнал systemd доступен только на Linux.\n"
		} else {
			b, commandErr := exec.CommandContext(r.Context(), "journalctl", "--no-pager", "--output=short-iso", "--lines", strconv.Itoa(lines), "--unit", name).Output()
			output, err = string(b), commandErr
		}
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "logs_read_failed", "Не удалось прочитать журнал")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": redact.Text(output)})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.store.Audit(r.Context(), limit)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "audit_read_failed", "Не удалось прочитать audit log")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	path, err := s.backups.Create(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "backup_failed", "Не удалось создать резервную копию")
		return
	}
	id := filepath.Base(path)
	audit(s, r, "backup.create", "backup", id, "success", "ordinary backup without private keys")
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "download_url": "/api/v1/system/backup/" + id})
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	path, err := s.backups.Resolve(r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "backup_not_found", "Резервная копия не найдена")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := backup.Copy(w, path); err != nil {
		s.logger.Error("backup download", "error", err)
	}
}

func wbStatus() map[string]any {
	return map[string]any{"supported": runtime.GOOS == "linux" && runtime.GOARCH == "amd64", "installed": fileExists("/opt/olcrtc-panel/wb/node_modules/playwright")}
}
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
