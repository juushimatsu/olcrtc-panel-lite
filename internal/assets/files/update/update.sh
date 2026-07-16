#!/usr/bin/env bash
set -euo pipefail
umask 077

ACTION=${1:-}
BUNDLE=${2:-}
REPOSITORY=$(cat /etc/olcrtc-panel/repository 2>/dev/null || echo "openlibrecommunity/olcrtc-panel-lite")
RELEASES=/var/lib/olcrtc-panel/releases
ARCH=$(dpkg --print-architecture)
case "$ARCH" in amd64|arm64) ;; *) echo "unsupported architecture" >&2; exit 1 ;; esac

install_bundle() {
    [[ "$BUNDLE" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "invalid bundle ID" >&2; exit 1; }
    target="$RELEASES/$BUNDLE"
    work=$(mktemp -d "$RELEASES/.update-XXXXXX")
    trap 'rm -rf "$work"' EXIT
    base="https://github.com/$REPOSITORY/releases/download/$BUNDLE"
    for file in manifest.json SHA256SUMS "olcrtc-panel-linux-$ARCH" "olcrtc-linux-$ARCH"; do curl -fsSL "$base/$file" -o "$work/$file"; done
    (cd "$work"; grep "  olcrtc-panel-linux-$ARCH$" SHA256SUMS | sha256sum -c -; grep "  olcrtc-linux-$ARCH$" SHA256SUMS | sha256sum -c -)
    install -d -m 0700 "$target"
    install -m 0755 "$work/olcrtc-panel-linux-$ARCH" "$target/olcrtc-panel"
    install -m 0755 "$work/olcrtc-linux-$ARCH" "$target/olcrtc"
    install -m 0600 "$work/manifest.json" "$target/manifest.json"
    current=$(readlink -f "$RELEASES/current" || true)
    mapfile -t active < <(systemctl list-units 'olcrtc-instance@*.service' --state=active --no-legend | awk '{print $1}')
    [ -n "$current" ] && ln -sfn "$current" "$RELEASES/previous"
    ln -sfn "$target" "$RELEASES/current"
    ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
    ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
    /usr/local/bin/olcrtc-panel assets install --root /
    systemctl daemon-reload
    systemctl restart olcrtc-panel.service
    sleep 3
    failed=false
    systemctl is-active --quiet olcrtc-panel.service || failed=true
    if ! $failed; then
        for unit in "${active[@]}"; do
            if ! systemctl restart "$unit" || ! systemctl is-active --quiet "$unit"; then failed=true; break; fi
        done
    fi
    if $failed; then
        [ -n "$current" ] || { echo "update failed and no previous bundle is available" >&2; exit 1; }
        ln -sfn "$current" "$RELEASES/current"
        ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
        ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
        systemctl restart olcrtc-panel.service
        for unit in "${active[@]}"; do systemctl restart "$unit" || true; done
        echo "new bundle failed health checks; rollback completed" >&2
        exit 1
    fi
}

rollback() {
    previous=$(readlink -f "$RELEASES/previous" || true)
    [ -x "$previous/olcrtc-panel" ] || { echo "previous bundle is unavailable" >&2; exit 1; }
    current=$(readlink -f "$RELEASES/current" || true)
    ln -sfn "$previous" "$RELEASES/current"
    [ -n "$current" ] && ln -sfn "$current" "$RELEASES/previous"
    ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
    ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
    systemctl restart olcrtc-panel.service
}

case "$ACTION" in install) install_bundle ;; rollback) rollback ;; *) echo "usage: update.sh install <bundle>|rollback" >&2; exit 2 ;; esac
