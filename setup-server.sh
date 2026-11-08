#!/bin/sh
# Prepare a fresh Debian VPS for app-host: packages, swap, Caddy, Subduction binaries, server notes.
#
#   curl -fsSL <raw-url>/setup-server.sh | sh
#
# Then, from the app-host repo: ./install.sh && ./extras/subduction-app.sh
# Safe to re-run. If /usr/local/bin/subduction_cli already exists (e.g. you scp'd a build), compiling is skipped.
set -eu

SWAP_GB="${SWAP_GB:-2}"
SUBDUCTION_VERSION="0.22.0"
INGEST_VERSION="0.9.0"

say() { printf '\n==> %s\n' "$*"; }
[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
grep -q '^ID=debian' /etc/os-release || echo "warning: not Debian; continuing anyway"

say "1/5 packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates openssl git rsync nodejs npm caddy >/dev/null

say "2/5 swap (${SWAP_GB} GB)"
if ! swapon --show | grep -q .; then
  fallocate -l "${SWAP_GB}G" /swapfile && chmod 600 /swapfile
  mkswap /swapfile >/dev/null && swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
else
  echo "swap already on"
fi

say "3/5 binaries"
if [ -x /usr/local/bin/subduction_cli ] && [ -x /usr/local/bin/automerge-subduction-ingest ]; then
  echo "found in /usr/local/bin, skipping build"
else
  echo "compiling on this machine (one job at a time to fit in RAM + swap; this is the slow part)"
  apt-get install -y -qq build-essential pkg-config libssl-dev >/dev/null
  if ! command -v cargo >/dev/null 2>&1; then
    curl -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal >/dev/null
  fi
  # shellcheck disable=SC1091
  . "$HOME/.cargo/env"
  # Build on disk, not /tmp: Debian 13's /tmp is RAM-backed (half of RAM), and the build needs ~1.2 GB.
  BUILD_DIR=/var/tmp/subduction-build
  avail_mb=$(df -Pm /var/tmp | awk 'NR==2 {print $4}')
  [ "$avail_mb" -ge 2048 ] || { echo "need ~2 GB free on /var/tmp for the build, have ${avail_mb} MB"; exit 1; }
  CARGO_TARGET_DIR="$BUILD_DIR" CARGO_BUILD_JOBS=1 cargo install --locked --root /usr/local \
    "subduction_cli@${SUBDUCTION_VERSION}" "automerge_subduction_ingest@${INGEST_VERSION}"
  rm -rf "$BUILD_DIR"
fi
/usr/local/bin/subduction_cli --version

say "4/5 caddy"
[ -f /etc/caddy/Caddyfile ] || printf '{\n}\n' > /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1 || { caddy validate --config /etc/caddy/Caddyfile; exit 1; }
systemctl enable caddy >/dev/null 2>&1
systemctl reload caddy 2>/dev/null || systemctl restart caddy

say "5/5 server notes"
if [ -e /root/README.md ] && ! grep -q 'app-host' /root/README.md; then
  mv /root/README.md /root/README.old.md
  echo "moved the old notes to /root/README.old.md"
fi
if [ -e /root/README.md ]; then
  echo "/root/README.md exists, leaving it alone"
else
  cat > /root/README.md <<'README_EOF'
# Server notes

Debian VPS. Caddy (reverse proxy + TLS) is the only thing exposed to the internet. Everything else runs through **app-host**: each app is a locked-down user with a folder under `/srv/apps/NAME`, and each folder in its `sites/` becomes a subdomain.

## Apps and sites

| App | Subdomain | What | Limits |
|---|---|---|---|
| subduction | `wss://subduction.sparkstonepdx.com` | Subduction sync server | 700M / 100% CPU |
| push (planned) | TBD | VAPID push notifications | default 256M |
| analytics (planned) | TBD | analytics / game stats | default 256M |

List everything live: `app sync --all` prints each site and its port. Ports are assigned automatically (20000–29999) and recorded in `/etc/apps/ports`.

## app-host cheat sheet

- New app: `app create NAME [--memory 256M] [--cpu 50%] [--tasks 256]`. It prints the GitHub secrets, variables and a block to paste to Claude to write the deploy workflow.
- Delete app: `app delete NAME` (asks you to type the name)
- Re-sync by hand: `app sync NAME` (normally runs on its own when `sites/` changes)
- Folder layout: `/srv/apps/NAME/{sites,builds,data,logs}` plus `secrets.env` (600, loaded into running apps)
- A site folder with an executable `run` file is a running app: it gets `$PORT`, `$SITE`, `$SITE_HOST`, and runs as unit `site-APP--SUB.service`. Any other folder is served as static files (`200.html` = SPA fallback, `404.html` = not found).
- As the app user (`runuser -u app-NAME -- bash -l`): `app site deploy DIR SUB`, `app site list|rm|restart|logs SUB`
- Config: `/etc/apps/config` (base domain, reserved names, default limits)

## Caddy

- Main config: `/etc/caddy/Caddyfile` (imports `/etc/caddy/apps/*.caddy`, which app-sync writes; don't edit those by hand)
- Check syntax: `caddy validate --config /etc/caddy/Caddyfile`
- Apply changes: `systemctl reload caddy`
- Status / logs: `systemctl status caddy` / `journalctl -u caddy -f`
- Update: `apt update && apt install --only-upgrade caddy`

For something outside app-host, add a block to `/etc/caddy/Caddyfile`:

```
example.sparkstonepdx.com {
	reverse_proxy 127.0.0.1:PORT
}
```

Caddy gets and renews TLS certs on its own. The domain's DNS must point at this server first. Let's Encrypt allows 50 certs per week for sparkstonepdx.com, so lots of PR previews would want a wildcard cert (needs a DNS-provider plugin for Caddy).

## Subduction

- App folder: `/srv/apps/subduction`, user `app-subduction`
- Data: `/srv/apps/subduction/data/subduction` (redb + blobs)
- Identity key: `/srv/apps/subduction/data/subduction.key`. **Back this up**: it's the server's identity, and Keyhive grants point at it.
- Unit: `site-subduction--subduction.service` (`systemctl status|restart site-subduction--subduction`)
- Logs: `tail -f /srv/apps/subduction/logs/subduction.log`
- Binaries: `/usr/local/bin/subduction_cli`, `/usr/local/bin/automerge-subduction-ingest` (0.22.0 / 0.9.0)
- `--service-name` is the site's hostname (`$SITE_HOST`); clients must connect with exactly that host.
- glibc malloc tunables are set in the `run` script, so freed memory goes back to the OS.
- `--max-resident-trees` is deliberately not set (open upstream bugs inkandswitch/subduction #268, #333).

Upgrade:

```
cargo install --locked subduction_cli@NEW automerge_subduction_ingest@NEW   # on a build machine
scp ~/.cargo/bin/subduction_cli ~/.cargo/bin/automerge-subduction-ingest root@THIS_SERVER:/usr/local/bin/
cd ~/app-host && ./extras/subduction-app.sh    # rebuilds the release and restarts; data untouched
```

Backup:

```
systemctl stop site-subduction--subduction
tar czf subduction-$(date +%F).tar.gz -C /srv/apps/subduction data
systemctl start site-subduction--subduction
```

Migration kit (export from the old automerge-repo server, ingest, verify): `~/migration-kit`, see `RUNBOOK.md`. Ingest target is `--service-name subduction.sparkstonepdx.com`.

## Memory budget

1 GB RAM + 2 GB swap. App caps should add up to less than RAM. Subduction is 700M; lower it (`/etc/systemd/system/app-subduction.slice`, then `systemctl daemon-reload`) as push and analytics arrive.

## System

- Update everything: `apt update && apt upgrade`
- Services on boot: `systemctl list-unit-files --state=enabled`
- Swap: `/swapfile` (2 GB), check with `swapon --show`
- Memory per app: `systemctl status app-NAME.slice`
- If `/tmp` fills (it's RAM-backed on Debian 13): `rm -rf /tmp/cargo-install*`
README_EOF
  echo "wrote /root/README.md"
fi

echo
echo "Base setup done. Next, from the app-host repo:"
echo "  ./install.sh                  # the app host"
echo "  ./extras/subduction-app.sh    # Subduction as the first app"
