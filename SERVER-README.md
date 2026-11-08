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

- New app: `app create NAME [--repo owner/name]` (or the dashboard). It prints one `APP_HOST` secret for the repo and a ready-to-use deploy workflow.
- Runtime env vars/secrets: dashboard → app → Environment, or `app env set NAME KEY` (prompts for the value)
- Delete app: `app delete NAME` (asks you to type the name)
- Re-sync by hand: `app sync NAME` (normally runs on its own when `sites/` changes)
- Folder layout: `/srv/apps/NAME/{sites,builds,data,logs}` plus `secrets.env` (600, loaded into running apps)
- A site folder with an executable `run` file is a running app: it gets `$PORT`, `$SITE`, `$SITE_HOST`, and runs as unit `site-APP--SUB.service`. Any other folder is served as static files (`200.html` = SPA fallback, `404.html` = not found).
- As the app user (`runuser -u app-NAME -- bash -l`): `app site deploy DIR SUB`, `app site list|rm|restart|logs SUB`
- Dashboard: `https://<server ip>:8443` (self-signed, always on as the backup way in) and the address set on its Domains page. New login link: `app invite`
- Domains: `app grant list`, `app grant add APP domain|sub|host NAME`. Site folder `blog` = blog.<default domain>; `client.com` = exactly that.
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
