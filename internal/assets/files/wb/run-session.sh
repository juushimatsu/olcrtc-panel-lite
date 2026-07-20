#!/usr/bin/env bash
set -euo pipefail
umask 077

export DISPLAY=:97
export HOME=/var/lib/olcrtc-wb
export XDG_RUNTIME_DIR=/run/olcrtc-wb/xdg
export PLAYWRIGHT_BROWSERS_PATH=/opt/olcrtc-panel/wb/browsers

mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
rm -f /tmp/.X97-lock /tmp/.X11-unix/X97

cleanup() {
    jobs -pr | xargs -r kill 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_for_tcp() {
    local port=$1
    local name=$2
    for _ in $(seq 1 50); do
        if (: > "/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
            return 0
        fi
        sleep 0.1
    done
    echo "$name did not start listening on port $port" >&2
    return 1
}

Xvfb "$DISPLAY" -screen 0 1280x800x24 -nolisten tcp -ac >"$XDG_RUNTIME_DIR/xvfb.log" 2>&1 &
for _ in $(seq 1 50); do
    [ -S /tmp/.X11-unix/X97 ] && break
    sleep 0.1
done
[ -S /tmp/.X11-unix/X97 ] || { cat "$XDG_RUNTIME_DIR/xvfb.log" >&2; echo "Xvfb did not create display $DISPLAY" >&2; exit 1; }
openbox >"$XDG_RUNTIME_DIR/openbox.log" 2>&1 &
x11vnc -display "$DISPLAY" -rfbport 5907 -localhost -forever -shared -nopw -quiet -noxdamage >"$XDG_RUNTIME_DIR/x11vnc.log" 2>&1 &
wait_for_tcp 5907 x11vnc || { cat "$XDG_RUNTIME_DIR/x11vnc.log" >&2; exit 1; }
websockify --web=/usr/share/novnc 127.0.0.1:6080 127.0.0.1:5907 >"$XDG_RUNTIME_DIR/websockify.log" 2>&1 &
wait_for_tcp 6080 websockify || { cat "$XDG_RUNTIME_DIR/websockify.log" >&2; exit 1; }

/opt/olcrtc-panel/wb/node/bin/node /opt/olcrtc-panel/wb/worker.mjs /run/olcrtc-wb/job.json
