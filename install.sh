#!/bin/bash
# One-time setup of the app host. Run as root on the Debian VPS, from inside this repo.
#   ./install.sh
#   BASE_DOMAIN=example.com SSH_HOST=1.2.3.4 ./install.sh
# Safe to re-run (keeps /etc/apps/config if it exists).
set -euo pipefail
cd "$(dirname "$0")"
[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
command -v caddy >/dev/null || { echo "install Caddy first (setup-subduction.sh does)"; exit 1; }

BASE_DOMAIN="${BASE_DOMAIN:-sparkstonepdx.com}"
SSH_HOST="${SSH_HOST:-$(hostname -I | awk '{print $1}')}"

mkdir -p /etc/apps/state /etc/caddy/apps /srv/apps
chmod 700 /etc/apps; chmod 711 /srv/apps    # app users can enter their own folder but not list the others
touch /etc/apps/ports; chmod 600 /etc/apps/ports
if [ ! -f /etc/apps/config ]; then
cat > /etc/apps/config <<EOF
BASE_DOMAIN=$BASE_DOMAIN
SSH_HOST=$SSH_HOST
APPS_DIR=/srv/apps
PORTS_FILE=/etc/apps/ports
STATE_DIR=/etc/apps/state
CADDY_APPS_DIR=/etc/caddy/apps
# running apps get ports from this range (Subduction 8080, push 8090, analytics 8100 sit outside it)
PORT_MIN=20000
PORT_MAX=29999
# subdomains no app can claim
RESERVED_SUBDOMAINS="www subduction sync push analytics mail admin api"
# limits shared by everything one app runs
APP_MEMORY_MAX=256M
APP_CPU_QUOTA=50%
APP_TASKS_MAX=256
EOF
fi
chmod 600 /etc/apps/config

install -m 755 sbin/app-create sbin/app-sync sbin/app-delete /usr/local/sbin/
install -m 755 bin/app /usr/local/bin/
install -m 644 systemd/app-sync@.path systemd/app-sync@.service /etc/systemd/system/
install -m 644 logrotate/apps /etc/logrotate.d/apps
systemctl daemon-reload

grep -qxF 'import /etc/caddy/apps/*.caddy' /etc/caddy/Caddyfile || printf '\nimport /etc/caddy/apps/*.caddy\n' >> /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1 && systemctl reload caddy
echo "App host installed (base domain $BASE_DOMAIN, SSH host $SSH_HOST)."
echo "Each app's subdomains need DNS: easiest is one wildcard record, *.$BASE_DOMAIN -> this server."
echo "Create an app with: app-create NAME"
