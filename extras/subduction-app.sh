#!/bin/bash
# Run Subduction as a nook app (/srv/nook/subduction), served at https://subduction.<BASE_DOMAIN>.
# Run as root after nook-admin install. Re-run any time to upgrade: it redeploys whatever binary is at $SUBDUCTION_BIN.
#   SUBDUCTION_BIN=/usr/local/bin/subduction_cli ./extras/subduction-app.sh
# If the old standalone service from the old setup-subduction.sh exists, its key and data are moved in and it is retired.
set -euo pipefail
. /etc/nook/config
APPS_DIR="${APPS_DIR:-/srv/nook}"
[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
# every write inside the app's folder happens as the app user, so a symlink there can't point root anywhere
asu() { runuser -u "$U" -- "$@"; }
BIN="${SUBDUCTION_BIN:-/usr/local/bin/subduction_cli}"
SUB="${SUB:-subduction}"
A=subduction; U="nook-$A"; H="$APPS_DIR/$A"; HOST="$SUB.$BASE_DOMAIN"
[ -x "$BIN" ] || { echo "no Subduction binary at $BIN"; exit 1; }

echo "==> app"
if ! id "$U" >/dev/null 2>&1; then
  nook-admin create "$A" --memory 700M --cpu 100% --tasks 512 > "/root/nook-$A-github.txt"
  chmod 600 "/root/nook-$A-github.txt"
  echo "created (GitHub deploy details saved to /root/nook-$A-github.txt)"
fi
asu mkdir -p -m 700 "$H/data/subduction"

echo "==> identity key"
if ! asu test -s "$H/data/subduction.key"; then
  if [ -s /etc/subduction/key ]; then src="cat /etc/subduction/key"; msg="kept existing key from /etc/subduction/key"
  else src="openssl rand -hex 32"; msg="generated a new key"; fi
  $src | asu sh -c 'umask 077; cat > "$1"' sh "$H/data/subduction.key"
  echo "$msg"
fi

echo "==> Keyhive contact card"
# Apps need the card to add this server to their docs as a relay; the dashboard shows it. Made from the key with
# @keyhive/keyhive in a throwaway folder, as the app user, so only the public card is written.
KEYHIVE="${KEYHIVE_VERSION:-0.4.0-alpha.1}"
if command -v node >/dev/null && command -v npm >/dev/null; then
  W=$(asu mktemp -d)
  install -o "$U" -m 644 "$(dirname "$0")/subduction-card.mjs" "$W/card.mjs"
  if asu sh -c 'cd "$1" && npm install --silent --no-audit --no-fund "@keyhive/keyhive@$2" >/dev/null 2>&1 && node card.mjs "$3" > card.json && [ -s card.json ] && umask 022 && cp card.json "$4"' \
      sh "$W" "$KEYHIVE" "$H/data/subduction.key" "$H/data/subduction-card.json"; then
    echo "saved to $H/data/subduction-card.json (keyhive $KEYHIVE)"
  else
    echo "couldn't make the card; the dashboard says so. Re-run once node can reach npm."
  fi
  rm -rf "$W"
else
  echo "skipped: no node here. Install Node and re-run to show the card in the dashboard."
fi

echo "==> retire the standalone service (if any)"
if [ -f /etc/systemd/system/subduction.service ]; then
  systemctl disable --now subduction >/dev/null 2>&1 || true
  if [ -d /var/lib/subduction ] && [ -z "$(asu ls -A "$H/data/subduction")" ]; then
    tar -C /var/lib/subduction -cf - . | asu tar -C "$H/data/subduction" -xf -
    echo "moved data from /var/lib/subduction (left the original in place as a backup)"
  fi
  rm -f /etc/systemd/system/subduction.service; systemctl daemon-reload
fi
if grep -qx -F "$HOST {" /etc/caddy/Caddyfile; then
  cp /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.bak-$(date +%Y%m%d%H%M%S)"
  awk -v h="$HOST {" '$0==h {skip=1; next} skip && $0=="}" {skip=0; next} !skip' /etc/caddy/Caddyfile > /etc/caddy/Caddyfile.new
  mv /etc/caddy/Caddyfile.new /etc/caddy/Caddyfile
  systemctl reload caddy || true
  echo "removed the old $HOST block from /etc/caddy/Caddyfile (backup kept)"
fi

echo "==> deploy"
R="$H/builds/subduction-release"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
install -m 755 "$BIN" "$T/subduction_cli"
cat > "$T/run" <<'RUN'
#!/bin/sh
# glibc otherwise keeps freed memory (tested 277 MB -> 19 MB after storing 135 MB of files)
export GLIBC_TUNABLES=glibc.malloc.arena_max=2:glibc.malloc.trim_threshold=131072:glibc.malloc.mmap_threshold=131072
export RUST_LOG="${RUST_LOG:-info}"
exec ./subduction_cli server --socket "127.0.0.1:$PORT" --data-dir "$HOME/data/subduction" \
  --key-file "$HOME/data/subduction.key" --service-name "$SITE_HOST"
RUN
chmod 755 "$T/run"
tar -C "$T" -cf - . | asu sh -c 'rm -rf "$1" && mkdir -p "$1" && tar -C "$1" -xf -' sh "$R"
# the deploy's own live check can finish before the sites/ watcher fires; sync here either way
runuser -u "$U" -- bash -lc "cd ~ && nook site deploy $R $SUB" || true
nook-admin sync "$A"
echo
echo "Subduction is live at wss://$HOST  (logs: /var/log/nook/$A/$SUB.log)"
