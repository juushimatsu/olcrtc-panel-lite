#!/usr/bin/env bash
set -euo pipefail
umask 077

REPOSITORY=${OLCRTC_PANEL_REPO:-juushimatsu/olcrtc-panel-lite}
CONFIG=/etc/olcrtc-panel/config.yaml
RELEASES=/var/lib/olcrtc-panel/releases
MODE=install
# Keep this distinct from VERSION, which is defined by /etc/os-release.
RELEASE_VERSION=""
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

installation_complete() {
    [ -x /usr/local/bin/olcrtc-panel ] &&
        [ -f "$CONFIG" ] &&
        [ -f /etc/systemd/system/olcrtc-panel.service ] &&
        [ -f /var/lib/olcrtc-panel/tls/server.crt ] &&
        [ -f /var/lib/olcrtc-panel/panel.db ] &&
        systemctl is-active --quiet olcrtc-panel.service
}

valid_port() {
    case "$1" in
        ''|*[!0-9]*) return 1 ;;
    esac
    [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

valid_listen() {
    local value=$1
    [[ "$value" =~ ^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]*):([0-9]+)$ ]] || return 1
    valid_port "${BASH_REMATCH[2]}"
}

listen_port() {
    local value=$1
    printf '%s\n' "${value##*:}"
}

listen_host() {
    local value=$1 host=${1%:*}
    host=${host#[}
    host=${host%]}
    printf '%s\n' "$host"
}

valid_mount_path() {
    local value=$1 allow_root=$2 segment remainder
    if [ "$value" = / ]; then
        [ "$allow_root" = true ]
        return
    fi
    [[ "$value" =~ ^(/[A-Za-z0-9._~-]+)+$ ]] || return 1
    remainder=${value#/}
    while [ -n "$remainder" ]; do
        segment=${remainder%%/*}
        [ "$segment" != . ] && [ "$segment" != .. ] || return 1
        if [ "$remainder" = "$segment" ]; then
            break
        fi
        remainder=${remainder#*/}
    done
}

paths_overlap() {
    local left=$1 right=$2
    [ "$left" = "$right" ] || [[ "$left" == "$right/"* ]] || [[ "$right" == "$left/"* ]]
}

valid_public_origin() {
    local value=$1 authority port
    [ -z "$value" ] && return 0
    [[ "$value" =~ ^https://(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+)(:[0-9]+)?$ ]] || return 1
    authority=${value#https://}
    if [[ "$authority" =~ :([0-9]+)$ ]]; then
        port=${BASH_REMATCH[1]}
        valid_port "$port"
    fi
}

config_value() {
    local key=$1
    [ -f "$CONFIG" ] || return 0
    awk -v wanted="$key" '
        $0 ~ "^[[:space:]]*" wanted ":[[:space:]]*" {
            line = $0
            sub(/^[^:]*:[[:space:]]*/, "", line)
            sub(/[[:space:]]+#.*$/, "", line)
            gsub(/^"|"$/, "", line)
            print line
            exit
        }
    ' "$CONFIG"
}

panel_health_url_from_values() {
    local listen=$1 panel_path=$2 host port
    host=$(listen_host "$listen")
    port=$(listen_port "$listen")
    case "$host" in
        ''|0.0.0.0) host=127.0.0.1 ;;
        ::) host=::1 ;;
    esac
    [[ "$host" == *:* ]] && host="[$host]"
    [ "$panel_path" = / ] || panel_path="$panel_path/"
    printf 'https://%s:%s%s\n' "$host" "$port" "$panel_path"
}

panel_health_url() {
    local binary=${1:-/usr/local/bin/olcrtc-panel} output
    if [ -x "$binary" ]; then
        if output=$("$binary" health-url --config "$CONFIG" 2>&1); then
            [ -n "$output" ] || { echo "health-url returned an empty URL" >&2; return 1; }
            printf '%s\n' "$output"
            return 0
        fi
        case "$output" in
            *'unknown command "health-url"'*) ;;
            *) printf '%s\n' "$output" >&2; return 1 ;;
        esac
    fi
    panel_health_url_from_values "$PANEL_LISTEN" "$PANEL_PATH"
}

loopback_listen() {
    local host
    host=$(listen_host "$1")
    case "$host" in 127.*|::1|localhost) return 0 ;; *) return 1 ;; esac
}

public_panel_url() {
    local origin=$1 public_ip=$2 public_port=$3 panel_path=$4 host=$public_ip
    if [ -n "$origin" ]; then
        printf '%s%s\n' "$origin" "$panel_path"
        return
    fi
    [[ "$host" == *:* ]] && host="[$host]"
    printf 'https://%s:%s%s\n' "$host" "$public_port" "$panel_path"
}

tcp_port_in_use() {
    local port=$1
    ss -H -ltn | awk -v wanted="$port" '
        {
            address = $4
            sub(/^.*:/, "", address)
            if (address == wanted) {
                found = 1
                exit
            }
        }
        END { exit found ? 0 : 1 }
    '
}

choose_panel_port() {
    local preferred=$1
    local span=55536
    local seed start offset candidate

    if ! tcp_port_in_use "$preferred"; then
        printf '%s\n' "$preferred"
        return 0
    fi

    seed=$(((RANDOM << 15) | RANDOM))
    start=$((10000 + seed % span))
    for ((offset = 0; offset < span; offset++)); do
        candidate=$((10000 + (start - 10000 + offset) % span))
        if ! tcp_port_in_use "$candidate"; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

wait_for_panel() {
    local url=$1
    local attempt

    for ((attempt = 0; attempt < 20; attempt++)); do
        if systemctl is-active --quiet olcrtc-panel.service &&
            curl -kfsS --connect-timeout 1 --max-time 2 "$url" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

prepare_automation_profiles() {
    id olcrtc-wb >/dev/null 2>&1 || return 0
    install -d -m 0700 -o olcrtc-wb -g olcrtc-wb \
        /var/lib/olcrtc-wb \
        /var/lib/olcrtc-wb/profiles \
        /var/lib/olcrtc-wb/profiles/wbstream \
        /var/lib/olcrtc-wb/profiles/telemost
}

repair_release_permissions() {
    local current

    id olcrtc >/dev/null 2>&1 || return 0
    [ -d /var/lib/olcrtc-panel ] || return 0
    chown root:olcrtc /var/lib/olcrtc-panel
    chmod 0710 /var/lib/olcrtc-panel
    if [ -d "$RELEASES" ]; then
        chown root:olcrtc "$RELEASES"
        chmod 0710 "$RELEASES"
    fi
    current=$(readlink -f "$RELEASES/current" 2>/dev/null || true)
    [ -n "$current" ] && [ -d "$current" ] || return 0
    chown root:olcrtc "$current"
    chmod 0710 "$current"
    if [ -f "$current/olcrtc-panel" ]; then
        chown root:root "$current/olcrtc-panel"
        chmod 0750 "$current/olcrtc-panel"
    fi
    if [ -f "$current/olcrtc" ]; then
        chown root:olcrtc "$current/olcrtc"
        chmod 0750 "$current/olcrtc"
    fi
}

repair_instance_permissions() {
    local directory instance_id runtime file

    id olcrtc >/dev/null 2>&1 || return 0
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

while [ $# -gt 0 ]; do
    case "$1" in
        --install) MODE=install ;;
        --update) MODE=update ;;
        --status) MODE=status ;;
        --reset-credentials) MODE=reset-credentials ;;
        --regenerate-cert) MODE=regenerate-cert ;;
        --uninstall) MODE=uninstall ;;
        --version) shift; RELEASE_VERSION=${1:-}; [ -n "$RELEASE_VERSION" ] || { echo "--version requires a value" >&2; exit 2; } ;;
        --non-interactive) : ;;
        --configure-firewall) CONFIGURE_FIREWALL=true ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
    shift
done

if [ -n "$RELEASE_VERSION" ]; then
    [[ "$RELEASE_VERSION" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "Invalid release version: $RELEASE_VERSION" >&2; exit 2; }
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
        [ -f "$CONFIG" ] && awk -F': ' '/^listen:|^public_ip:|^public_port:|^public_origin:|^panel_path:|^subscription_path:/{gsub(/"/,"",$2); print $1"="$2}' "$CONFIG"
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

if [ -x /usr/local/bin/olcrtc-panel ] && [ "$MODE" = install ] && [ -z "$RELEASE_VERSION" ]; then
    if installation_complete; then
        repair_release_permissions
        repair_instance_permissions
        echo "olcrtc-panel is already installed. Current status:"
        echo "version=$(/usr/local/bin/olcrtc-panel version)"
        systemctl is-active olcrtc-panel.service 2>/dev/null || true
        echo "Use --update to install a new verified bundle."
        exit 0
    fi
    echo "Incomplete olcrtc-panel installation detected; resuming repair."
fi

[ -r /etc/os-release ] || { echo "Unsupported system" >&2; exit 1; }
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-}" in ubuntu) [[ "${VERSION_ID:-}" == "22.04" || "${VERSION_ID:-}" == "24.04" ]] || echo "Warning: Ubuntu ${VERSION_ID:-unknown} is outside the tested matrix" ;; debian) [[ "${VERSION_ID:-}" == "12" ]] || echo "Warning: Debian ${VERSION_ID:-unknown} is outside the tested matrix" ;; *) echo "Only Ubuntu and Debian are supported" >&2; exit 1 ;; esac
command -v systemctl >/dev/null || { echo "systemd is required" >&2; exit 1; }

ARCH=$(dpkg --print-architecture)
case "$ARCH" in amd64|arm64) ;; *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;; esac

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl iproute2 jq

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

RELEASES_API="https://api.github.com/repos/$REPOSITORY/releases"
if [ -n "$RELEASE_VERSION" ]; then
    RELEASE_API="$RELEASES_API/tags/$RELEASE_VERSION"
else
    RELEASE_API="$RELEASES_API/latest"
fi

if ! curl -fsSL --retry 3 --connect-timeout 15 \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2022-11-28' \
    "$RELEASE_API" -o "$WORK/release.json"; then
    if [ -n "$RELEASE_VERSION" ]; then
        echo "GitHub Release '$RELEASE_VERSION' was not found in $REPOSITORY." >&2
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
[ -n "$BUNDLE" ] || BUNDLE=${RELEASE_VERSION:-$(date -u +%Y%m%d%H%M%S)}
[[ "$BUNDLE" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "Manifest contains an invalid bundle_id" >&2; exit 1; }

if [ "$MODE" = update ] && [ -x /usr/local/bin/olcrtc-panel ] && [ -x /usr/lib/olcrtc-panel/update.sh ]; then
    /usr/lib/olcrtc-panel/update.sh install "$BUNDLE"
    exit
fi

CONFIG_EXISTS=false
[ -f "$CONFIG" ] && CONFIG_EXISTS=true
if $CONFIG_EXISTS; then
    PANEL_LISTEN=$(config_value listen)
    PUBLIC_IP=$(config_value public_ip)
    PUBLIC_PORT=$(config_value public_port)
    PUBLIC_ORIGIN=$(config_value public_origin)
    PANEL_PATH=$(config_value panel_path)
    SUBSCRIPTION_PATH=$(config_value subscription_path)
    PANEL_LISTEN=${PANEL_LISTEN:-0.0.0.0:${PUBLIC_PORT:-8443}}
    PUBLIC_PORT=${PUBLIC_PORT:-$(listen_port "$PANEL_LISTEN")}
    PANEL_PATH=${PANEL_PATH:-/}
    SUBSCRIPTION_PATH=${SUBSCRIPTION_PATH:-/sub}
    valid_listen "$PANEL_LISTEN" || { echo "Invalid listen in existing config: $PANEL_LISTEN" >&2; exit 2; }
    valid_port "$PUBLIC_PORT" || { echo "Invalid public_port in existing config: $PUBLIC_PORT" >&2; exit 2; }
else
    PUBLIC_ORIGIN=${OLCRTC_PUBLIC_ORIGIN:-}
    PANEL_PATH=${OLCRTC_PANEL_PATH:-/}
    SUBSCRIPTION_PATH=${OLCRTC_SUBSCRIPTION_PATH:-/sub}
    valid_public_origin "$PUBLIC_ORIGIN" || { echo "Invalid OLCRTC_PUBLIC_ORIGIN: expected an HTTPS origin without a path" >&2; exit 2; }
    valid_mount_path "$PANEL_PATH" true || { echo "Invalid OLCRTC_PANEL_PATH" >&2; exit 2; }
    valid_mount_path "$SUBSCRIPTION_PATH" false || { echo "Invalid OLCRTC_SUBSCRIPTION_PATH" >&2; exit 2; }
    if [ "$PANEL_PATH" != / ] && paths_overlap "$PANEL_PATH" "$SUBSCRIPTION_PATH"; then
        echo "Panel and subscription paths must not overlap" >&2
        exit 2
    fi
    if [ -n "${OLCRTC_LISTEN:-}" ]; then
        PANEL_LISTEN=$OLCRTC_LISTEN
        valid_listen "$PANEL_LISTEN" || { echo "Invalid OLCRTC_LISTEN" >&2; exit 2; }
        PANEL_PORT=$(listen_port "$PANEL_LISTEN")
        ss -H -ltn >/dev/null || { echo "Could not inspect listening TCP ports" >&2; exit 1; }
        tcp_port_in_use "$PANEL_PORT" && { echo "TCP port $PANEL_PORT from OLCRTC_LISTEN is already in use" >&2; exit 1; }
    else
        PREFERRED_PORT=${OLCRTC_PUBLIC_PORT:-8443}
        valid_port "$PREFERRED_PORT" || { echo "Invalid panel port: $PREFERRED_PORT" >&2; exit 2; }
        ss -H -ltn >/dev/null || { echo "Could not inspect listening TCP ports" >&2; exit 1; }
        PANEL_PORT=$(choose_panel_port "$PREFERRED_PORT") || { echo "Could not find a free TCP port" >&2; exit 1; }
        PANEL_LISTEN="0.0.0.0:$PANEL_PORT"
        if [ "$PANEL_PORT" != "$PREFERRED_PORT" ]; then
            echo "TCP port $PREFERRED_PORT is already in use; selected free port $PANEL_PORT."
        fi
    fi
    PUBLIC_PORT=${OLCRTC_PUBLIC_PORT:-$PANEL_PORT}
    valid_port "$PUBLIC_PORT" || { echo "Invalid OLCRTC_PUBLIC_PORT" >&2; exit 2; }
fi
PANEL_PORT=$(listen_port "$PANEL_LISTEN")

id olcrtc >/dev/null 2>&1 || useradd --system --home-dir /var/lib/olcrtc --shell /usr/sbin/nologin olcrtc
id olcrtc-wb >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/olcrtc-wb --shell /usr/sbin/nologin olcrtc-wb
install -d -m 0710 -o root -g olcrtc /etc/olcrtc-panel
install -d -m 0710 -o root -g olcrtc /var/lib/olcrtc-panel "$RELEASES"
install -d -m 0750 -o root -g olcrtc /etc/olcrtc-panel/instances
install -d -m 0750 -o olcrtc -g olcrtc /var/lib/olcrtc
install -d -m 0700 -o olcrtc-wb -g olcrtc-wb /var/lib/olcrtc-wb
install -d -m 0750 -o olcrtc-wb -g olcrtc-wb /run/olcrtc-wb
repair_instance_permissions

TARGET="$RELEASES/$BUNDLE"
install -d -m 0710 -o root -g olcrtc "$TARGET"
install -m 0750 -o root -g root "$WORK/olcrtc-panel-linux-$ARCH" "$TARGET/olcrtc-panel"
install -m 0750 -o root -g olcrtc "$WORK/olcrtc-linux-$ARCH" "$TARGET/olcrtc"
install -m 0600 "$WORK/manifest.json" "$TARGET/manifest.json"
ln -sfn "$TARGET" "$RELEASES/current"
ln -sfn "$RELEASES/current/olcrtc-panel" /usr/local/bin/olcrtc-panel
ln -sfn "$RELEASES/current/olcrtc" /usr/local/bin/olcrtc
printf '%s\n' "$REPOSITORY" > /etc/olcrtc-panel/repository
chmod 0600 /etc/olcrtc-panel/repository

PUBLIC_IP=${PUBLIC_IP:-${OLCRTC_PUBLIC_IP:-}}
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')
fi
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP=$(ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')
fi
[ -n "$PUBLIC_IP" ] || { echo "Could not detect public IP. Set OLCRTC_PUBLIC_IP." >&2; exit 1; }

if ! $CONFIG_EXISTS; then
    cat > "$CONFIG" <<EOF
listen: "$PANEL_LISTEN"
public_ip: "$PUBLIC_IP"
public_port: $PUBLIC_PORT
public_origin: "$PUBLIC_ORIGIN"
panel_path: "$PANEL_PATH"
subscription_path: "$SUBSCRIPTION_PATH"
trusted_proxies:
  - "127.0.0.1/32"
  - "::1/128"
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
prepare_automation_profiles
if [ -f /var/lib/olcrtc-panel/panel.db ]; then
    CREDS="credentials=preserved"
else
    CREDS=$(/usr/local/bin/olcrtc-panel credentials reset --config "$CONFIG")
fi
CERTS=$(/usr/local/bin/olcrtc-panel certificate ensure --config "$CONFIG")
systemctl daemon-reload
systemctl enable --now olcrtc-panel.service
HEALTH_URL=$(panel_health_url /usr/local/bin/olcrtc-panel)
if ! wait_for_panel "$HEALTH_URL"; then
    echo "olcRTC Panel Lite failed health check at $HEALTH_URL." >&2
    systemctl --no-pager --full status olcrtc-panel.service >&2 || true
    journalctl -u olcrtc-panel.service -n 50 --no-pager >&2 || true
    exit 1
fi

if loopback_listen "$PANEL_LISTEN"; then
    echo "Firewall rule skipped: panel listen address is loopback-only."
elif $CONFIGURE_FIREWALL; then
    if command -v ufw >/dev/null 2>&1; then ufw allow "$PANEL_PORT/tcp"; elif command -v firewall-cmd >/dev/null 2>&1; then firewall-cmd --permanent --add-port="$PANEL_PORT/tcp"; firewall-cmd --reload; fi
else
    if command -v ufw >/dev/null 2>&1; then echo "Firewall command: sudo ufw allow $PANEL_PORT/tcp"; elif command -v firewall-cmd >/dev/null 2>&1; then echo "Firewall command: sudo firewall-cmd --permanent --add-port=$PANEL_PORT/tcp && sudo firewall-cmd --reload"; fi
fi

echo
echo "olcRTC Panel Lite installed"
echo "url=$(public_panel_url "$PUBLIC_ORIGIN" "$PUBLIC_IP" "$PUBLIC_PORT" "$PANEL_PATH")"
printf '%s\n' "$CREDS"
printf '%s\n' "$CERTS"
echo "No olcRTC instance was created. Create the first one in the UI."
echo "Verify the CA fingerprint in this terminal before trusting ca.crt."
