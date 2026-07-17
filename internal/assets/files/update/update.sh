#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

ACTION=${1:-}
BUNDLE=${2:-}
REPOSITORY=$(cat /etc/olcrtc-panel/repository 2>/dev/null || echo "juushimatsu/olcrtc-panel-lite")
RELEASES=/var/lib/olcrtc-panel/releases
STATE_FILE=/run/olcrtc-panel-update-state.json
WORK_DIR=
ARCH=$(dpkg --print-architecture)
case "$ARCH" in amd64|arm64) ;; *) echo "unsupported architecture" >&2; exit 1 ;; esac

write_state() {
    printf '{"phase":"%s","message":"%s","percent":%s,"updated_at":%s}\n' "$1" "$2" "$3" "$(date +%s)" > "$STATE_FILE"
}

cleanup() {
    local status=$?
    if [ -n "$WORK_DIR" ]; then
        rm -rf "$WORK_DIR" || true
    fi
    if [ "$status" -ne 0 ]; then
        write_state error "Операция обновления завершилась с ошибкой" 0
    fi
    exit "$status"
}

trap cleanup EXIT

set_bundle_permissions() {
    local directory=$1
    [ -d "$directory" ] || return 0
    chown root:olcrtc "$directory"
    chmod 0710 "$directory"
    if [ -f "$directory/olcrtc-panel" ]; then
        chown root:root "$directory/olcrtc-panel"
        chmod 0750 "$directory/olcrtc-panel"
    fi
    if [ -f "$directory/olcrtc" ]; then
        chown root:olcrtc "$directory/olcrtc"
        chmod 0750 "$directory/olcrtc"
    fi
}

repair_instance_permissions() {
    local directory instance_id runtime file
    [ -d /etc/olcrtc-panel ] || return 0
    chown root:olcrtc /etc/olcrtc-panel
    chmod 0710 /etc/olcrtc-panel
    [ -d /etc/olcrtc-panel/instances ] || return 0
    chown root:olcrtc /etc/olcrtc-panel/instances
    chmod 0750 /etc/olcrtc-panel/instances
    for directory in /etc/olcrtc-panel/instances/[0-9]*; do
        [ -d "$directory" ] || continue
        chown root:olcrtc "$directory"
        chmod 0750 "$directory"
        for file in "$directory/config.yaml" "$directory/key.hex"; do
            [ -f "$file" ] || continue
            chown root:olcrtc "$file"
            chmod 0640 "$file"
        done
        instance_id=${directory##*/}
        runtime="/var/lib/olcrtc/$instance_id"
        if [ -d "$runtime" ]; then
            chown olcrtc:olcrtc "$runtime"
            chmod 0750 "$runtime"
        fi
        if [ -d "$runtime/data" ]; then
            chown olcrtc:olcrtc "$runtime/data"
            chmod 0750 "$runtime/data"
        fi
    done
}

install_bundle() {
    [[ "$BUNDLE" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "invalid bundle ID" >&2; exit 1; }
    write_state preparing "Подготовка обновления" 5
    install -d -m 0710 -o root -g olcrtc /var/lib/olcrtc-panel "$RELEASES"
    target="$RELEASES/$BUNDLE"
    WORK_DIR=$(mktemp -d "$RELEASES/.update-XXXXXX")
    work=$WORK_DIR
    base="https://github.com/$REPOSITORY/releases/download/$BUNDLE"
    write_state downloading "Загрузка файлов release bundle" 15
    for file in manifest.json SHA256SUMS "olcrtc-panel-linux-$ARCH" "olcrtc-linux-$ARCH"; do curl -fsSL "$base/$file" -o "$work/$file"; done
    write_state verifying "Проверка SHA-256 загруженных файлов" 35
    (cd "$work"; grep "  olcrtc-panel-linux-$ARCH$" SHA256SUMS | sha256sum -c -; grep "  olcrtc-linux-$ARCH$" SHA256SUMS | sha256sum -c -)
    write_state installing "Установка проверенного bundle" 50
    install -d -m 0710 -o root -g olcrtc "$target"
    install -m 0750 -o root -g root "$work/olcrtc-panel-linux-$ARCH" "$target/olcrtc-panel"
    install -m 0750 -o root -g olcrtc "$work/olcrtc-linux-$ARCH" "$target/olcrtc"
    install -m 0600 "$work/manifest.json" "$target/manifest.json"
    current=$(readlink -f "$RELEASES/current" || true)
    [ -n "$current" ] && set_bundle_permissions "$current"
    repair_instance_permissions
    mapfile -t active < <(systemctl list-units 'olcrtc-instance@*.service' --state=active --no-legend | awk '{print $1}')
    write_state switching "Переключение на выбранный bundle" 65
    [ -n "$current" ] && ln -sfn "$current" "$RELEASES/previous"
    ln -sfn "$target" "$RELEASES/current"
    ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
    ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
    /usr/local/bin/olcrtc-panel assets install --root /
    systemctl daemon-reload
    write_state restarting "Перезапуск панели и активных инстансов" 80
    systemctl restart olcrtc-panel.service
    sleep 3
    write_state checking "Проверка состояния служб" 90
    failed=false
    systemctl is-active --quiet olcrtc-panel.service || failed=true
    if ! $failed; then
        for unit in "${active[@]}"; do
            if ! systemctl restart "$unit" || ! systemctl is-active --quiet "$unit"; then failed=true; break; fi
        done
    fi
    if $failed; then
        [ -n "$current" ] || { echo "update failed and no previous bundle is available" >&2; exit 1; }
        write_state rollback "Проверка не пройдена, восстановление предыдущего bundle" 70
        set_bundle_permissions "$current"
        ln -sfn "$current" "$RELEASES/current"
        ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
        ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
        systemctl restart olcrtc-panel.service
        for unit in "${active[@]}"; do systemctl restart "$unit" || true; done
        echo "new bundle failed health checks; rollback completed" >&2
        exit 1
    fi
    write_state completed "Bundle установлен, службы успешно запущены" 100
}

rollback() {
    write_state preparing "Подготовка rollback" 10
    previous=$(readlink -f "$RELEASES/previous" || true)
    [ -n "$previous" ] && set_bundle_permissions "$previous"
    [ -x "$previous/olcrtc-panel" ] || { echo "previous bundle is unavailable" >&2; exit 1; }
    current=$(readlink -f "$RELEASES/current" || true)
    repair_instance_permissions
    write_state switching "Переключение на предыдущий bundle" 55
    ln -sfn "$previous" "$RELEASES/current"
    [ -n "$current" ] && ln -sfn "$current" "$RELEASES/previous"
    ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
    ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
    /usr/local/bin/olcrtc-panel assets install --root /
    systemctl daemon-reload
    write_state restarting "Перезапуск панели после rollback" 80
    systemctl restart olcrtc-panel.service
    write_state completed "Rollback завершён" 100
}

case "$ACTION" in install) install_bundle ;; rollback) rollback ;; *) echo "usage: update.sh install <bundle>|rollback" >&2; exit 2 ;; esac
