#!/usr/bin/env bash
set -euo pipefail

STATE_FILE=/run/olcrtc-wb-components-state.json
INSTALL_DIR=/opt/olcrtc-panel/wb
write_state() { printf '{"phase":"%s","message":"%s","percent":%s,"updated_at":%s}\n' "$1" "$2" "$3" "$(date +%s)" > "$STATE_FILE"; }

write_state stopping "Остановка браузерной сессии" 10
systemctl stop olcrtc-wb-session.service 2>/dev/null || true
packages=()
if [ -f "$INSTALL_DIR/packages-installed-by-panel" ]; then
    mapfile -t packages < "$INSTALL_DIR/packages-installed-by-panel"
fi
write_state cleaning "Удаление профиля и cookies" 40
rm -rf /var/lib/olcrtc-wb /run/olcrtc-wb "$INSTALL_DIR"
if [ "${#packages[@]}" -gt 0 ]; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get purge -y "${packages[@]}" || true
fi
write_state completed "Компоненты автоматизации удалены" 100
