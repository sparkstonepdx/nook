#!/bin/bash
# Run Subduction as an app-host app (/srv/apps/subduction), served at https://subduction.<BASE_DOMAIN>.
# Run as root after ./install.sh. Re-run any time to upgrade: it redeploys whatever binary is at $SUBDUCTION_BIN.
#   SUBDUCTION_BIN=/usr/local/bin/subduction_cli ./extras/subduction-app.sh
# If the old standalone service from the old setup-subduction.sh exists, its key and data are moved in and it is retired.
set -euo pipefail
. /etc/apps/config
[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
BIN="${SUBDUCTION_BIN:-/usr/local/bin/subduction_cli}"
SUB="${SUB:-subduction}"
A=subduction; U="app-$A"; H="$APPS_DIR/$A"; HOST="$SUB.$BASE_DOMAIN"
[ -x "$BIN" ] || { echo "no Subduction binary at $BIN"; exit 1; }

echo "==> app"
if ! id "$U" >/dev/null 2>&1; then
  app-create "$A" --memory 700M --cpu 100% --tasks 512 > "/root/app-$A-github.txt"
  chmod 600 "/root/app-$A-github.txt"
  echo "created (GitHub deploy details saved to /root/app-$A-github.txt)"
fi
install -d -o "$U" -g "$U" -m 700 "$H/data" "$H/data/subduction"

echo "==> identity key"
if [ ! -s "$H/data/subduction.key" ]; then
  if [ -s /etc/subduction/key ]; then cp /etc/subduction/key "$H/data/subduction.key"; echo "kept existing key from /etc/subduction/key"
  else openssl rand -hex 32 > "$H/data/subduction.key"; echo "generated a new key"; fi
  chown "$U:$U" "$H/data/subduction.key"; chmod 600 "$H/data/subduction.key"
fi

echo "==> retire the standalone service (if any)"
if [ -f /etc/systemd/system/subduction.service ]; then
  systemctl disable --now subduction >/dev/null 2>&1 || true
  if [ -d /var/lib/subduction ] && [ -z "$(ls -A "$H/data/subduction")" ]; then
    cp -a /var/lib/subduction/. "$H/data/subduction/"; chown -R "$U:$U" "$H/data/subduction"
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
rm -rf "$R"; install -d -o "$U" -g "$U" -m 700 "$R"
install -o "$U" -g "$U" -m 755 "$BIN" "$R/subduction_cli"
cat > "$R/run" <<'RUN'
#!/bin/sh
# glibc otherwise keeps freed memory (tested 277 MB -> 19 MB after storing 135 MB of files)
export GLIBC_TUNABLES=glibc.malloc.arena_max=2:glibc.malloc.trim_threshold=131072:glibc.malloc.mmap_threshold=131072
export RUST_LOG="${RUST_LOG:-info}"
exec ./subduction_cli server --socket "127.0.0.1:$PORT" --data-dir "$HOME/data/subduction" \
  --key-file "$HOME/data/subduction.key" --service-name "$SITE_HOST"
RUN
chown "$U:$U" "$R/run"; chmod 755 "$R/run"
# the deploy's own live check can finish before the sites/ watcher fires; sync here either way
runuser -u "$U" -- bash -lc "cd ~ && app site deploy $R $SUB" || true
app-sync "$A"
echo
echo "Subduction is live at wss://$HOST  (logs: /var/log/apps/$A/$SUB.log)"
