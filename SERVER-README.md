# Server notes

Debian VPS. Caddy (reverse proxy + TLS) is the only thing exposed to the internet. Everything else runs through **app-host**: each app is a locked-down user with a folder under `/srv/apps/NAME`, and each folder in its `sites/` becomes a site.

## Dashboard

- `https://<server ip>:8443` always works (self-signed certificate): first setup, and the way in if DNS breaks.
- Its own address is set on its Domains page (e.g. `admin.sparkstonepdx.com`).
- New login link: `app-host invite`
- Services: `app-host-web` (unprivileged dashboard) and `app-host-helper` (root, accepts a fixed set of operations from the dashboard only)

## Commands (root)

- `app-host create NAME [--repo owner/name] [--memory 256M] [--cpu 50%] [--tasks 256]`: prints the one `APP_HOST` secret for the repo and a ready deploy workflow
- `app-host delete NAME`
- `app-host sync NAME | --all` (runs on its own after every deploy)
- `app-host grant list` / `app-host grant add|rm APP domain|sub|host NAME`
- `app-host env set NAME KEY` (prompts), `app-host env list NAME [--values]`, `app-host env unset NAME KEY`
- `app-host update` / `app-host update --check`, `app-host version`

App users (and their deploy keys) only get `app site deploy|rm|restart|list|logs`. SSH for `app-*` users is forced through `app ssh-gate`: an rsync upload into `builds/` and `app site …`, nothing else.

## Where things live

| What | Where |
|---|---|
| App folders (700, the app's own) | `/srv/apps/NAME/{sites,builds,data}` |
| Settings | `/etc/apps/config` (default domain, server IP, limits), `/etc/apps/dashboard.env`, `/etc/apps/update.env` |
| Which hostnames each app may use | `/etc/apps/grants` |
| Runtime env per app | `/etc/apps/env/NAME.env` (root-only) |
| Deploy public keys | `/etc/ssh/app-host-keys/app-NAME` |
| Logs of running sites | `/var/log/apps/NAME/SITE.log` (rotated hourly at 10 MB) |
| Static copies Caddy serves | `/var/lib/app-host/www/NAME/SITE` |
| Dashboard logins | `/var/lib/app-host-web/users.json` |
| Generated Caddy config | `/etc/caddy/apps/*.caddy`, `/etc/caddy/admin.caddy` (don't edit) |
| Catch-all 404 page | `/etc/caddy/catchall/index.html` (yours to edit) |

## Updates

- `app-host update` installs the latest signed release (verifies the ed25519 signature and checksums, swaps both binaries, restarts the dashboard; running sites keep running).
- A daily check shows "update available" on the dashboard and in the login message. Put `UPDATE_NOTIFY_URL=https://ntfy.sh/<topic>` (or a Discord/Slack webhook) in `/etc/apps/update.env` to get pinged.
- While the repo is private, add `GITHUB_TOKEN=<read-only token for app-host>` to `/etc/apps/update.env`.

## Caddy

- Main config: `/etc/caddy/Caddyfile` (imports the generated files). Admin API is on a root-only socket, not `localhost:2019`.
- Check / apply: `caddy validate --config /etc/caddy/Caddyfile` then `systemctl reload caddy`
- Logs: `journalctl -u caddy -f`

## Subduction (optional)

- Build: `SUBDUCTION=1 sh setup-server.sh`, then `./extras/subduction-app.sh` (re-run to upgrade).
- Data: `/srv/apps/subduction/data/subduction`, key `/srv/apps/subduction/data/subduction.key` (**back this up**)
- Logs: `tail -f /var/log/apps/subduction/subduction.log`

## System

- Update everything: `apt update && apt upgrade`
- Swap: `/swapfile` (2 GB), check with `swapon --show`
- Memory per app: `systemctl status app-NAME.slice`
- Firewall: allow 22, 80, 443 (tcp + udp) and 8443
