#!/usr/bin/env bash
set -euo pipefail
umask 077

REPOSITORY=${OLCRTC_PANEL_REPO:-juushimatsu/olcrtc-panel-lite}
CONFIG=/etc/olcrtc-panel/config.yaml
RELEASES=/var/lib/olcrtc-panel/releases
MODE=install
VERSION=""
CONFIGURE_FIREWALL=false

[[ "$REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || {
    echo "Invalid GitHub repository: $REPOSITORY" >&2
    exit 2
}

usage() {
    cat <<'EOF'
Usage: install.sh [--install|--update|--status|--reset-credentials|--regenerate-cert|--uninstall]
                  [--version <bundle>] [--non-interactive] [--configure-firewall]
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --install) MODE=install ;;
        --update) MODE=update ;;
        --status) MODE=status ;;
        --reset-credentials) MODE=reset-credentials ;;
        --regenerate-cert) MODE=regenerate-cert ;;
        --uninstall) MODE=uninstall ;;
        --version) shift; VERSION=${1:-}; [ -n "$VERSION" ] || { echo "--version requires a value" >&2; exit 2; } ;;
        --non-interactive) : ;;
        --configure-firewall) CONFIGURE_FIREWALL=true ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
    shift
done

if [ -n "$VERSION" ]; then
    [[ "$VERSION" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "Invalid release version: $VERSION" >&2; exit 2; }
fi

[ "$(id -u)" -eq 0 ] || { echo "Run installer as root" >&2; exit 1; }

if [ "$MODE" = uninstall ]; then
    curl -fsSL "https://raw.githubusercontent.com/$REPOSITORY/master/uninstall.sh" | bash
    exit
fi

if [ "$MODE" = status ]; then
    if command -v olcrtc-panel >/dev/null 2>&1; then
        echo "version=$(olcrtc-panel version)"
        systemctl --no-pager --full status olcrtc-panel.service || true
        [ -f "$CONFIG" ] && awk -F': ' '/^public_ip:|^public_port:/{gsub(/"/,"",$2); print $1"="$2}' "$CONFIG"
    else
        echo "olcrtc-panel is not installed"
    fi
    exit
fi

if [ "$MODE" = reset-credentials ]; then
    [ -x /usr/local/bin/olcrtc-panel ] || { echo "Panel is not installed" >&2; exit 1; }
    /usr/local/bin/olcrtc-panel credentials reset --config "$CONFIG"
    exit
fi

if [ "$MODE" = regenerate-cert ]; then
    [ -x /usr/local/bin/olcrtc-panel ] || { echo "Panel is not installed" >&2; exit 1; }
    /usr/local/bin/olcrtc-panel certificate regenerate --config "$CONFIG"
    systemctl restart olcrtc-panel.service
    exit
fi

if [ -x /usr/local/bin/olcrtc-panel ] && [ "$MODE" = install ] && [ -z "$VERSION" ]; then
    echo "olcrtc-panel is already installed. Current status:"
    echo "version=$(/usr/local/bin/olcrtc-panel version)"
    systemctl is-active olcrtc-panel.service 2>/dev/null || true
    echo "Use --update to install a new verified bundle."
    exit 0
fi

[ -r /etc/os-release ] || { echo "Unsupported system" >&2; exit 1; }
. /etc/os-release
case "${ID:-}" in ubuntu) [[ "${VERSION_ID:-}" == "22.04" || "${VERSION_ID:-}" == "24.04" ]] || echo "Warning: Ubuntu ${VERSION_ID:-unknown} is outside the tested matrix" ;; debian) [[ "${VERSION_ID:-}" == "12" ]] || echo "Warning: Debian ${VERSION_ID:-unknown} is outside the tested matrix" ;; *) echo "Only Ubuntu and Debian are supported" >&2; exit 1 ;; esac
command -v systemctl >/dev/null || { echo "systemd is required" >&2; exit 1; }

ARCH=$(dpkg --print-architecture)
case "$ARCH" in amd64|arm64) ;; *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;; esac

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl jq

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

RELEASES_API="https://api.github.com/repos/$REPOSITORY/releases"
if [ -n "$VERSION" ]; then
    RELEASE_API="$RELEASES_API/tags/$VERSION"
else
    RELEASE_API="$RELEASES_API/latest"
fi

if ! curl -fsSL --retry 3 --connect-timeout 15 \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2022-11-28' \
    "$RELEASE_API" -o "$WORK/release.json"; then
    if [ -n "$VERSION" ]; then
        echo "GitHub Release '$VERSION' was not found in $REPOSITORY." >&2
    else
        echo "No published GitHub Release was found in $REPOSITORY." >&2
    fi
    echo "The installer requires a release bundle. Run the 'daily upstream bundle' workflow:" >&2
    echo "https://github.com/$REPOSITORY/actions/workflows/daily-upstream.yml" >&2
    exit 1
fi

RELEASE_TAG=$(jq -r '.tag_name // empty' "$WORK/release.json")
[ -n "$RELEASE_TAG" ] || { echo "GitHub returned a release without tag_name" >&2; exit 1; }

for file in manifest.json SHA256SUMS "olcrtc-panel-linux-$ARCH" "olcrtc-linux-$ARCH"; do
    ASSET_URL=$(jq -r --arg name "$file" '[.assets[]? | select(.name == $name) | .browser_download_url][0] // empty' "$WORK/release.json")
    [ -n "$ASSET_URL" ] || {
        echo "GitHub Release '$RELEASE_TAG' is incomplete: missing asset '$file'." >&2
        exit 1
    }
    curl -fL --retry 3 --connect-timeout 15 "$ASSET_URL" -o "$WORK/$file"
done
(cd "$WORK"; grep "  olcrtc-panel-linux-$ARCH$" SHA256SUMS | sha256sum -c -; grep "  olcrtc-linux-$ARCH$" SHA256SUMS | sha256sum -c -)
BUNDLE=$(jq -r '.bundle_id // empty' "$WORK/manifest.json")
[ -n "$BUNDLE" ] || BUNDLE=${VERSION:-$(date -u +%Y%m%d%H%M%S)}
[[ "$BUNDLE" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "Manifest contains an invalid bundle_id" >&2; exit 1; }

if [ "$MODE" = update ] && [ -x /usr/local/bin/olcrtc-panel ] && [ -x /usr/lib/olcrtc-panel/update.sh ]; then
    /usr/lib/olcrtc-panel/update.sh install "$BUNDLE"
    exit
fi

id olcrtc >/dev/null 2>&1 || useradd --system --home-dir /var/lib/olcrtc --shell /usr/sbin/nologin olcrtc
id olcrtc-wb >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/olcrtc-wb --shell /usr/sbin/nologin olcrtc-wb
install -d -m 0700 -o root -g root /etc/olcrtc-panel /var/lib/olcrtc-panel "$RELEASES"
install -d -m 0750 -o root -g olcrtc /etc/olcrtc-panel/instances
install -d -m 0750 -o olcrtc -g olcrtc /var/lib/olcrtc
install -d -m 0700 -o olcrtc-wb -g olcrtc-wb /var/lib/olcrtc-wb

TARGET="$RELEASES/$BUNDLE"
install -d -m 0700 -o root -g root "$TARGET"
install -m 0755 "$WORK/olcrtc-panel-linux-$ARCH" "$TARGET/olcrtc-panel"
install -m 0755 "$WORK/olcrtc-linux-$ARCH" "$TARGET/olcrtc"
install -m 0600 "$WORK/manifest.json" "$TARGET/manifest.json"
ln -sfn "$TARGET" "$RELEASES/current"
ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
printf '%s\n' "$REPOSITORY" > /etc/olcrtc-panel/repository
chmod 0600 /etc/olcrtc-panel/repository

PUBLIC_IP=${OLCRTC_PUBLIC_IP:-}
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')
fi
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP=$(ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')
fi
[ -n "$PUBLIC_IP" ] || { echo "Could not detect public IP. Set OLCRTC_PUBLIC_IP." >&2; exit 1; }

if [ ! -f "$CONFIG" ]; then
    cat > "$CONFIG" <<EOF
listen: "0.0.0.0:8443"
public_ip: "$PUBLIC_IP"
public_port: 8443
database_path: "/var/lib/olcrtc-panel/panel.db"
master_key_path: "/etc/olcrtc-panel/master.key"
instances_dir: "/etc/olcrtc-panel/instances"
runtime_dir: "/var/lib/olcrtc"
tls_dir: "/var/lib/olcrtc-panel/tls"
backup_dir: "/var/lib/olcrtc-panel/backups"
release_dir: "/var/lib/olcrtc-panel/releases"
olcrtc_binary: "/usr/local/bin/olcrtc"
systemd_enabled: true
max_instances: 20
cookie_name: "olcrtc_panel_session"
hsts: false
release_manifest_url: "https://github.com/$REPOSITORY/releases/latest/download/manifest.json"
upstream_sha: "$(jq -r '.upstream_sha // ""' "$WORK/manifest.json")"
panel_version: "$(jq -r '.panel_version // "unknown"' "$WORK/manifest.json")"
EOF
    chmod 0600 "$CONFIG"
fi

/usr/local/bin/olcrtc-panel assets install --root /
if [ -f /var/lib/olcrtc-panel/panel.db ]; then
    CREDS="credentials=preserved"
else
    CREDS=$(/usr/local/bin/olcrtc-panel credentials reset --config "$CONFIG")
fi
CERTS=$(/usr/local/bin/olcrtc-panel certificate ensure --config "$CONFIG")
systemctl daemon-reload
systemctl enable --now olcrtc-panel.service

if $CONFIGURE_FIREWALL; then
    if command -v ufw >/dev/null 2>&1; then ufw allow 8443/tcp; elif command -v firewall-cmd >/dev/null 2>&1; then firewall-cmd --permanent --add-port=8443/tcp; firewall-cmd --reload; fi
else
    if command -v ufw >/dev/null 2>&1; then echo "Firewall command: sudo ufw allow 8443/tcp"; elif command -v firewall-cmd >/dev/null 2>&1; then echo "Firewall command: sudo firewall-cmd --permanent --add-port=8443/tcp && sudo firewall-cmd --reload"; fi
fi

echo
echo "olcRTC Panel Lite installed"
echo "url=https://$PUBLIC_IP:8443"
printf '%s\n' "$CREDS"
printf '%s\n' "$CERTS"
echo "No olcRTC instance was created. Create the first one in the UI."
echo "Verify the CA fingerprint in this terminal before trusting ca.crt."
