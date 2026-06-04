#!/usr/bin/env bash
# Install tally(a)wg: binary + config + state dir + systemd service.
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "run as root (sudo ./install.sh)"; exit 1; }
HERE="$(cd "$(dirname "$0")" && pwd)"

# Pick a binary: prebuilt linux binary, prebuilt host binary, or build from source.
BIN=""
for c in "$HERE/tallyawg-linux-amd64" "$HERE/tallyawg"; do
  [ -x "$c" ] && BIN="$c" && break
done
if [ -z "$BIN" ]; then
  if command -v go >/dev/null 2>&1; then
    echo ">> building from source"
    ( cd "$HERE" && CGO_ENABLED=0 go build -ldflags "-s -w" -o tallyawg . )
    BIN="$HERE/tallyawg"
  else
    echo "no prebuilt binary and Go is not installed; run 'make linux' first" >&2
    exit 1
  fi
fi

install -m 0755 "$BIN" /usr/local/bin/tallyawg
echo ">> installed /usr/local/bin/tallyawg"

mkdir -p /etc/tallyawg /var/lib/tallyawg
if [ ! -f /etc/tallyawg/tallyawg.env ]; then
  install -m 0644 "$HERE/tallyawg.env.example" /etc/tallyawg/tallyawg.env
  echo ">> wrote /etc/tallyawg/tallyawg.env (edit it to set your interface)"
else
  echo ">> kept existing /etc/tallyawg/tallyawg.env"
fi

install -m 0644 "$HERE/systemd/tallyawg.service" /etc/systemd/system/tallyawg.service
systemctl daemon-reload
systemctl enable --now tallyawg.service
echo ">> enabled tallyawg.service"
echo
echo "done. terminal:  tallyawg            (or: tallyawg report)"
echo "      web page:  http://127.0.0.1:8082  (put behind your own reverse proxy + auth)"
