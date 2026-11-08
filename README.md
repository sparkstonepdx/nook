# app-host

A tiny Surge replacement plus process host for one Debian box with Caddy. Each repo gets its own isolated app folder; every folder inside its `sites/` is a live site, named by its hostname.

```
/srv/apps/<app>/sites/blog/              ->  https://blog.<default domain>
/srv/apps/<app>/sites/client.com/        ->  https://client.com   (+ www.client.com redirecting to it)
/srv/apps/<app>/sites/pr-12.client.com/  ->  https://pr-12.client.com
    index.html, 200.html, 404.html       static site (200.html = SPA fallback, 404.html = not found)
    run (executable)                     running app: started as a service on $PORT, Caddy proxies to it
```

An app can only publish hostnames it has been granted (Domains page, or `app grant`).

Create a folder: it's live within seconds. Delete it: it's gone. Swap it: a running app restarts on the new code.

## Install (once, as root)

On a fresh Debian VPS, run in this order:

```
sh setup-server.sh            # packages, swap, Caddy, Subduction binaries, /root/README.md
./install.sh                  # app-host
./extras/subduction-app.sh    # Subduction as the first app (subduction.sparkstonepdx.com)
```

Details:

1. Caddy must already be installed (`setup-server.sh` does it).
2. `./install.sh` (set `BASE_DOMAIN=` / `SSH_HOST=` to override; `SSH_HOST` must be the server's public IP). Settings live in `/etc/apps/config`.
3. Open the setup link it prints (`https://<ip>:8443/invite?...`, self-signed certificate: accept the warning) and create your admin login.
4. On the Domains page: set the dashboard address (e.g. `admin.sparkstonepdx.com`), grant domains to apps, and add the DNS records it lists. Ports 80, 443 and 8443 must be open.

## Dashboard

![Apps](docs/dashboard-apps.png)

Lists every app and site (state, port, memory, whether it's live), and runs create, delete, sync, restart/stop/start, remove site, reload Caddy, and shows logs. Caddy serves it at:

- `https://<server ip>:8443`, always, with a self-signed certificate: for first setup, and as the way in if DNS breaks.
- The dashboard address you set on the Domains page, once DNS points here. No app can claim that hostname.

Logins are per user (username + password, scrypt-hashed in `/etc/apps/dashboard/users.json`), with HMAC-signed session cookies that last 30 days. Every user has full access.

- First admin: `install.sh` prints a one-time setup link (24h). Open it, pick a username and password.
- More users: Users page → "make invite link", send it privately. One use, 24h.
- Locked out: `app invite` on the server prints a fresh link.
- Removing a user or changing a password logs out their sessions immediately.
- Forms only work when posted from the dashboard itself (Origin check + SameSite cookies). Failed logins are slowed to one per second.

### How it's built

- `dashboard/server.mjs`: a no-dependency Node server on 127.0.0.1:19999, running as root (unit `app-dashboard`). It's a JSON API under `/api/*` (the only thing that runs commands) and serves the UI from `dashboard/public`.
- `dashboard/ui`: the UI source, Solid + `@sparkstone/css`, built with Vite into `dashboard/public`. The build is committed so the server never builds anything; `install.sh` copies it.
- Changing the UI: `cd dashboard/ui && npm install && npm run dev` (proxies `/api` to a local server on :19999), then `npm run build`, commit `dashboard/public`, and run `./install.sh` on the server.
- The UI's JavaScript runs with your admin session, so it only ever comes from this repo, never from an app's deploy. Requests must be JSON from the dashboard's own origin; responses carry a strict Content-Security-Policy.

## Domains

![Domains](docs/dashboard-domains.png)

- **Default domain** (`BASE_DOMAIN`): site folders without a dot are subdomains of it.
- **Grants**, per app, in `/etc/apps/grants`:
  - `domain client.com`: `client.com` and every `*.client.com`
  - `sub client.com`: `*.client.com` only (use this for a shared domain)
  - `host blog.client.com`: exactly that hostname

  New apps get `sub <default domain>`. Several apps can share a domain; the first to publish a hostname keeps it.
- A root domain an app owns (`domain` or `host` grant) with no `www.` folder gets `www.` redirecting to it. A real `www.` site in any app replaces the redirect.
- The Domains page lists the DNS records each grant needs.

## Admin commands (root)

- `app create NAME [--memory 256M] [--cpu 50%] [--tasks 256]`: makes user `app-NAME` and `/srv/apps/NAME`, then prints the GitHub secrets/variables and a block of instructions to paste to Claude for writing the deploy workflow. The deploy private key is shown once and not stored on the server.
- `app delete NAME`: stops everything the app runs, removes its sites, user, and folder (asks for confirmation).
- `app sync NAME | --all`: regenerate an app's routes and services. Runs automatically when `sites/` changes; run it by hand after editing `/etc/apps/config`.
- `app grant list [APP]`, `app grant add|rm APP domain|sub|host NAME`: which hostnames an app may use (re-syncs the app).
- `app invite`: one-time dashboard login link.

## App commands (as the app user, e.g. from GitHub Actions over SSH)

```
app site deploy DIR SITE        # atomic swap into sites/SITE; exits 2 if SITE didn't go live
app site rm SITE                # SITE: "blog" (subdomain of the default domain) or "client.com" (exact)
app site restart SITE           # running apps
app site list
app site logs SITE [N]
```

## What each app gets

| Path | Purpose |
|---|---|
| `sites/` | one folder per hostname (world-readable so Caddy can serve it) |
| `builds/` | private upload area for rsync |
| `data/` | private persistent data for running apps (survives redeploys) |
| `logs/<sub>.log` | stdout/stderr of running apps (rotated at 10 MB, 3 kept) |
| `secrets.env` | `KEY=value` lines, loaded into every running app's environment |

Running apps listen on `127.0.0.1:$PORT`; `$SITE`, `$SITE_HOST` and `$HOME` are set too. Ports come from 20000-29999, are assigned the first time a site has a `run` file, and are freed when it's removed. Everything one app runs shares a systemd slice, capped at 256M memory / 50% CPU / 256 tasks by default (set per app with `app create` options, or edit `/etc/systemd/system/app-NAME.slice`).

## Rules and isolation

- Folder name = hostname, and it must be covered by one of the app's grants. The first app to claim a hostname keeps it; other apps' attempts are skipped and reported. `RESERVED_SUBDOMAINS` (of the default domain) and the dashboard address can't be claimed by anyone.
- App folders are `711`: an app user can't list or read another app's private files (`secrets.env`, `data/`, `builds/`, `.ssh/`). Files inside `sites/` are readable by path, because Caddy has to serve them, so treat sites as public.
- The deploy key is limited with `restrict` (no port forwarding, no pty).
- Running apps are systemd services with `ProtectSystem=strict`, `NoNewPrivileges`, private /tmp, and only their own app folder visible.
- Each new subdomain gets its own Let's Encrypt certificate, and Let's Encrypt allows 50 new certificates per registered domain per 7 days. Lots of short-lived PR previews can hit that.

## Tested

In an Ubuntu sandbox with real users, permissions, Caddy (local certs), and a stand-in for systemd: static sites, SPA fallback, 404 page, PR preview add/remove, a running Node app with `$PORT` and secrets, restart on redeploy, subdomain conflicts, reserved and invalid names, cross-app isolation, `app delete`, grants (domain/sub/host, revoke takes sites down), root domains with the automatic www redirect and another app's real www replacing it, the dashboard over the IP (self-signed) and over its own hostname, and the dashboard's forms in headless Chrome. Not tested: real systemd (the `sites/` watcher, the service sandboxing directives, the slice limits) and a real SSH deploy from GitHub Actions.
