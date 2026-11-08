#!/bin/bash
# One-time setup of the app host. Run as root on the Debian VPS from inside this repo.
#   ./install.sh                      (BASE_DOMAIN defaults to sparkstonepdx.com)
#   BASE_DOMAIN=example.com SSH_HOST=1.2.3.4 ./install.sh
set -euo pipefail
cd "$(dirname "$0")"
[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }

BASE_DOMAIN="${BASE_DOMAIN:-sparkstonepdx.com}"
SSH_HOST="${SSH_HOST:-$(hostname -I | awk '{print $1}')}"

mkdir -p /etc/apps /etc/caddy/apps /srv/apps
chmod 700 /etc/apps; chmod 711 /srv/apps    # app users can enter their own folder but not list others
touch /etc/apps/ports; chmod 600 /etc/apps/ports
# Ports already used by services on this box (Subduction 8080, reserved push 8090 / analytics 8100) are
# outside the app range, and anything listening at create time is skipped too.
if [ ! -f /etc/apps/config ]; then
cat > /etc/apps/config <<EOF
BASE_DOMAIN=$BASE_DOMAIN
SSH_HOST=$SSH_HOST
APPS_DIR=/srv/apps
PORTS_FILE=/etc/apps/ports
CADDY_APPS_DIR=/etc/caddy/apps
PORT_MIN=20000
PORT_MAX=29999
RESERVED_SUBDOMAINS="www subduction sync push analytics mail admin"
APP_MEMORY_MAX=256M
APP_CPU_QUOTA=50%
APP_TASKS_MAX=256
EOF
fi
chmod 600 /etc/apps/config

install -m 755 sbin/app-create sbin/app-caddy-sync sbin/app-delete /usr/local/sbin/
install -m 755 bin/app /usr/local/bin/
install -m 644 systemd/app-caddy@.path systemd/app-caddy@.service /etc/systemd/system/
systemctl daemon-reload

grep -qxF 'import /etc/caddy/apps/*.caddy' /etc/caddy/Caddyfile || printf '\nimport /etc/caddy/apps/*.caddy\n' >> /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1 && systemctl reload caddy
echo "app host installed. Create an app with: app-create NAME [SUBDOMAIN]"
