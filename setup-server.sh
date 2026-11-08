#!/bin/sh
# Prepare a fresh Debian VPS and install nook: packages, swap, Caddy, nook, server notes.
#
#   curl -fsSL https://raw.githubusercontent.com/sparkstonepdx/nook/main/setup-server.sh | sh   (public repo)
#   git clone ... && cd nook && sh setup-server.sh                                              (private repo: builds from source)
#
# SUBDUCTION=1 also builds the Subduction sync server (slow on 1 GB); then run ./extras/subduction-app.sh.
# Safe to re-run.
set -eu

SWAP_GB="${SWAP_GB:-2}"
REPO="${NOOK_TOKEN_REPO:-sparkstonepdx/nook}"
SUBDUCTION_VERSION="0.22.0"
INGEST_VERSION="0.9.0"

say() { printf '\n==> %s\n' "$*"; }
[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
grep -q '^ID=debian' /etc/os-release || echo "warning: not Debian; continuing anyway"

say "packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates openssl rsync caddy >/dev/null

say "swap (${SWAP_GB} GB)"
if ! swapon --show | grep -q .; then
  fallocate -l "${SWAP_GB}G" /swapfile && chmod 600 /swapfile
  mkswap /swapfile >/dev/null && swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
else
  echo "swap already on"
fi

say "caddy"
[ -f /etc/caddy/Caddyfile ] || printf '{\n}\n' > /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1 || { caddy validate --config /etc/caddy/Caddyfile; exit 1; }
systemctl enable caddy >/dev/null 2>&1
systemctl reload caddy 2>/dev/null || systemctl restart caddy

say "nook-admin"
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
if [ -d ./cmd/nook-admin ] && [ -f ./go.mod ]; then
  echo "building from this checkout"
  command -v go >/dev/null 2>&1 || apt-get install -y -qq golang-go >/dev/null
  ver=$(git describe --tags --always 2>/dev/null || echo dev)
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$work/nook-admin" ./cmd/nook-admin
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$work/nook" ./cmd/nook
else
  arch=$(dpkg --print-architecture)
  echo "downloading the latest release of $REPO ($arch)"
  rel=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest") || { echo "no release found (private repo? clone it and run this script from the checkout)"; exit 1; }
  url=$(printf '%s' "$rel" | grep -o '"browser_download_url": *"[^"]*_linux_'"$arch"'\.tar\.gz"' | sed 's/.*"\(https[^"]*\)"/\1/')
  sums=$(printf '%s' "$rel" | grep -o '"browser_download_url": *"[^"]*/checksums\.txt"' | sed 's/.*"\(https[^"]*\)"/\1/')
  curl -fsSL -o "$work/nook.tgz" "$url"
  curl -fsSL -o "$work/checksums.txt" "$sums"
  want=$(grep "$(basename "$url")" "$work/checksums.txt" | cut -d' ' -f1)
  got=$(sha256sum "$work/nook.tgz" | cut -d' ' -f1)
  [ "$want" = "$got" ] || { echo "checksum mismatch, stopping"; exit 1; }
  tar -C "$work" -xzf "$work/nook.tgz"
fi
"$work/nook-admin" install

if [ "${SUBDUCTION:-0}" = 1 ]; then
  say "subduction binaries"
  if [ -x /usr/local/bin/subduction_cli ] && [ -x /usr/local/bin/automerge-subduction-ingest ]; then
    echo "found in /usr/local/bin, skipping build"
  else
    echo "compiling on this machine (one job at a time to fit in RAM + swap; this is the slow part)"
    apt-get install -y -qq build-essential pkg-config libssl-dev nodejs npm >/dev/null
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
fi

say "server notes"
if [ -e /root/README.md ] && ! grep -q 'nook-helper' /root/README.md; then
  mv /root/README.md /root/README.old.md
  echo "moved the old notes to /root/README.old.md"
fi
if [ -e /root/README.md ]; then
  echo "/root/README.md exists, leaving it alone"
else
  cat > /root/README.md <<'README_EOF'
# Server notes

Debian VPS. Caddy (reverse proxy + TLS) is the only thing exposed to the internet. Everything else runs through **nook**: each app is a locked-down user with a folder under `/srv/nook/NAME`, and each folder in its `sites/` becomes a site.

## Dashboard

- `https://<server ip>:8443` always works (self-signed certificate): first setup, and the way in if DNS breaks.
- Its own address is set on its Domains page (e.g. `admin.sparkstonepdx.com`).
- New login link: `nook-admin invite`
- Services: `nook-web` (unprivileged dashboard) and `nook-helper` (root, accepts a fixed set of operations from the dashboard only)

## Commands (root)

- `nook-admin create NAME [--repo owner/name] [--memory 256M] [--cpu 50%] [--tasks 256]`: prints the one `NOOK_TOKEN` secret for the repo and a ready deploy workflow
- `nook-admin delete NAME`
- `nook-admin sync NAME | --all` (runs on its own after every deploy)
- `nook-admin grant list` / `nook-admin grant add|rm APP domain|sub|host NAME`
- `nook-admin env set NAME KEY` (prompts), `nook-admin env list NAME [--values]`, `nook-admin env unset NAME KEY`
- `nook-admin update` / `nook-admin update --check`, `nook-admin version`

App users (and their deploy keys) only get `nook site deploy|rm|restart|list|logs`. SSH for `nook-*` users is forced through `nook ssh-gate`: an rsync upload into `builds/` and `nook site …`, nothing else.

## Where things live

| What | Where |
|---|---|
| App folders (700, the app's own) | `/srv/nook/NAME/{sites,builds,data}` |
| Settings | `/etc/nook/config` (default domain, server IP, limits), `/etc/nook/dashboard.env`, `/etc/nook/update.env` |
| Which hostnames each app may use | `/etc/nook/grants` |
| Runtime env per app | `/etc/nook/env/NAME.env` (root-only) |
| Deploy public keys | `/etc/ssh/nook-keys/nook-NAME` |
| Logs of running sites | `/var/log/nook/NAME/SITE.log` (rotated hourly at 10 MB) |
| Static copies Caddy serves | `/var/lib/nook/www/NAME/SITE` |
| Dashboard logins | `/var/lib/nook-web/users.json` |
| Generated Caddy config | `/etc/caddy/nook/*.caddy`, `/etc/caddy/nook-dashboard.caddy` (don't edit) |
| Catch-all 404 page | `/etc/caddy/catchall/index.html` (yours to edit) |

## Updates

- `nook-admin update` installs the latest signed release (verifies the ed25519 signature and checksums, swaps both binaries, restarts the dashboard; running sites keep running).
- A daily check shows "update available" on the dashboard and in the login message. Put `UPDATE_NOTIFY_URL=https://ntfy.sh/<topic>` (or a Discord/Slack webhook) in `/etc/nook/update.env` to get pinged.
- While the repo is private, add `GITHUB_TOKEN=<read-only token for the nook repo>` to `/etc/nook/update.env`.

## Caddy

- Main config: `/etc/caddy/Caddyfile` (imports the generated files). Admin API is on a root-only socket, not `localhost:2019`.
- Check / apply: `caddy validate --config /etc/caddy/Caddyfile` then `systemctl reload caddy`
- Logs: `journalctl -u caddy -f`

## Subduction (optional)

- Build: `SUBDUCTION=1 sh setup-server.sh`, then `./extras/subduction-app.sh` (re-run to upgrade).
- Data: `/srv/nook/subduction/data/subduction`, key `/srv/nook/subduction/data/subduction.key` (**back this up**)
- Logs: `tail -f /var/log/nook/subduction/subduction.log`

## System

- Update everything: `apt update && apt upgrade`
- Swap: `/swapfile` (2 GB), check with `swapon --show`
- Memory per app: `systemctl status nook-NAME.slice`
- Firewall: allow 22, 80, 443 (tcp + udp) and 8443
README_EOF
  echo "wrote /root/README.md"
fi

echo
echo "Done. Notes are in /root/README.md."
