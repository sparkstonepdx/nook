# app-host

A tiny Surge replacement plus process host for one Debian box with Caddy. Each repo gets its own isolated app folder; every folder inside its `sites/` is a live subdomain.

```
/srv/apps/<app>/sites/<sub>/           ->  https://<sub>.sparkstonepdx.com
    index.html, 200.html, 404.html     static site (200.html = SPA fallback, 404.html = not found)
    run (executable)                   running app: started as a service on $PORT, Caddy proxies to it
```

Create a folder: it's live within seconds. Delete it: it's gone. Swap it: a running app restarts on the new code.

## Install (once, as root)

1. Caddy must already be installed (`setup-subduction.sh` does it).
2. Add a wildcard DNS record `*.sparkstonepdx.com` pointing at this server.
3. `./install.sh` (set `BASE_DOMAIN=` / `SSH_HOST=` to override). Settings live in `/etc/apps/config`.

## Admin commands (root)

- `app-create NAME`: makes user `app-NAME` and `/srv/apps/NAME`, then prints the GitHub secrets/variables and a block of instructions to paste to Claude for writing the deploy workflow. The deploy private key is shown once and not stored on the server.
- `app-delete NAME`: stops everything the app runs, removes its sites, user, and folder (asks for confirmation).
- `app-sync NAME | --all`: regenerate an app's routes and services. Runs automatically when `sites/` changes; run it by hand after editing `/etc/apps/config`.

## App commands (as the app user, e.g. from GitHub Actions over SSH)

```
app site deploy DIR SUBDOMAIN   # atomic swap into sites/SUBDOMAIN
app site rm SUBDOMAIN
app site restart SUBDOMAIN      # running apps
app site list
app site logs SUBDOMAIN [N]
```

## What each app gets

| Path | Purpose |
|---|---|
| `sites/` | one folder per subdomain (world-readable so Caddy can serve it) |
| `builds/` | private upload area for rsync |
| `data/` | private persistent data for running apps (survives redeploys) |
| `logs/<sub>.log` | stdout/stderr of running apps (rotated at 10 MB, 3 kept) |
| `secrets.env` | `KEY=value` lines, loaded into every running app's environment |

Running apps listen on `127.0.0.1:$PORT`. Ports come from 20000-29999, are assigned the first time a site has a `run` file, and are freed when it's removed. Everything one app runs shares a systemd slice capped by `APP_MEMORY_MAX` / `APP_CPU_QUOTA` / `APP_TASKS_MAX` (256M / 50% / 256 by default).

## Rules and isolation

- Subdomain = folder name: lowercase letters, digits, dashes. The first app to claim a name keeps it; other apps' attempts are skipped and reported. `RESERVED_SUBDOMAINS` in the config can't be claimed by anyone.
- App folders are `711`: an app user can't list or read another app's private files (`secrets.env`, `data/`, `builds/`, `.ssh/`). Files inside `sites/` are readable by path, because Caddy has to serve them, so treat sites as public.
- The deploy key is limited with `restrict` (no port forwarding, no pty).
- Running apps are systemd services with `ProtectSystem=strict`, `NoNewPrivileges`, private /tmp, and only their own app folder visible.
- Each new subdomain gets its own Let's Encrypt certificate, and Let's Encrypt allows 50 new certificates per registered domain per 7 days. Lots of short-lived PR previews can hit that.

## Tested

In an Ubuntu sandbox with real users, permissions, Caddy (local certs), and a stand-in for systemd: static sites, SPA fallback, 404 page, PR preview add/remove, a running Node app with `$PORT` and secrets, restart on redeploy, subdomain conflicts, reserved and invalid names, cross-app isolation, `app-delete`. Not tested: real systemd (the `sites/` watcher, the service sandboxing directives, the slice limits) and a real SSH deploy from GitHub Actions.
