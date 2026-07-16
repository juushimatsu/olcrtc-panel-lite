#!/usr/bin/env bash
set -euo pipefail
umask 077

PURGE=false
YES=false
while [ $# -gt 0 ]; do
    case "$1" in --purge) PURGE=true ;; --yes|--non-interactive) YES=true ;; -h|--help) echo "Usage: uninstall.sh [--purge] [--yes|--non-interactive]"; exit 0 ;; *) echo "Unknown option: $1" >&2; exit 2 ;; esac
    shift
done
[ "$(id -u)" -eq 0 ] || { echo "Run uninstaller as root" >&2; exit 1; }

confirm() {
    local prompt=$1
    local answer

    if ! exec 9<>/dev/tty 2>/dev/null; then
        echo "Cannot read confirmation: no interactive terminal. Re-run with --yes." >&2
        return 2
    fi
    printf '%s' "$prompt" >&9
    if ! IFS= read -r answer <&9; then
        exec 9>&-
        echo "Cannot read confirmation from the terminal. Re-run with --yes." >&2
        return 2
    fi
    exec 9>&-
    [[ "$answer" =~ ^[Yy]$ ]]
}

if ! $YES; then
    if confirm "Remove olcRTC Panel Lite? [y/N] "; then
        :
    else
        confirmation_status=$?
        [ "$confirmation_status" -eq 1 ] && exit 0
        exit "$confirmation_status"
    fi
fi

systemctl stop olcrtc-panel.service 2>/dev/null || true
mapfile -t units < <(systemctl list-unit-files 'olcrtc-instance@*.service' --no-legend 2>/dev/null | awk '{print $1}')
for unit in "${units[@]}"; do systemctl disable --now "$unit" 2>/dev/null || true; done
systemctl stop olcrtc-wb-session.service 2>/dev/null || true

BACKUP_DIR=/var/backups/olcrtc-panel
install -d -m 0700 -o root -g root "$BACKUP_DIR"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BACKUP="$BACKUP_DIR/recovery-$STAMP.tar.gz"
paths=()
for path in /etc/olcrtc-panel /var/lib/olcrtc-panel; do [ -e "$path" ] && paths+=("${path#/}"); done
if [ ${#paths[@]} -gt 0 ]; then
    tar -C / -czf "$BACKUP" "${paths[@]}"
    chmod 0600 "$BACKUP"
    echo "Recovery backup containing secrets: $BACKUP"
fi

rm -f /etc/systemd/system/olcrtc-panel.service /etc/systemd/system/olcrtc-instance@.service /etc/systemd/system/olcrtc-wb-session.service
rm -f /usr/local/bin/olcrtc-panel /usr/local/bin/olcrtc
rm -rf /usr/lib/olcrtc-panel /opt/olcrtc-panel /var/lib/olcrtc-wb /run/olcrtc-wb
rm -rf /etc/olcrtc-panel /var/lib/olcrtc-panel /var/lib/olcrtc
systemctl daemon-reload

if $PURGE; then
    remove_backups=false
    if $YES; then
        remove_backups=true
    else
        if confirm "Also delete all backups in $BACKUP_DIR? [y/N] "; then
            remove_backups=true
        else
            confirmation_status=$?
            [ "$confirmation_status" -eq 2 ] && exit "$confirmation_status"
        fi
    fi
    $remove_backups && rm -rf "$BACKUP_DIR"
fi

for account in olcrtc-wb olcrtc; do
    if id "$account" >/dev/null 2>&1 && ! find / -xdev -user "$account" -print -quit 2>/dev/null | grep -q .; then userdel "$account" 2>/dev/null || true; fi
done
echo "olcRTC Panel Lite removed"
