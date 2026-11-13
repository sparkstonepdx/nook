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
- Keyhive identity: the dashboard's Server section (or `nook-admin subduction`) shows the address, peer id and contact card apps need, each with a Copy. The script makes the card (`data/subduction-card.json`) from the key when Node is installed; it's the JSON card that subduction_cli 0.22 and automerge-repo-keyhive 0.7 use (set `KEYHIVE_VERSION` to make another).

## System

- Update everything: `apt update && apt upgrade`
- Swap: `/swapfile` (2 GB), check with `swapon --show`
- Memory per app: `systemctl status nook-NAME.slice`
- Firewall: allow 22, 80, 443 (tcp + udp) and 8443
