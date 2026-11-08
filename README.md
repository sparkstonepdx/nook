# app-host

Run apps and static sites on one Debian box behind Caddy, deployed from GitHub. Each repo gets an isolated app (its own Linux user and folder); every folder in its `sites/` is a live site, named by its hostname.

```
/srv/apps/<app>/sites/blog/              ->  https://blog.<default domain>
/srv/apps/<app>/sites/client.com/        ->  https://client.com   (+ www.client.com redirecting to it)
/srv/apps/<app>/sites/pr-12.client.com/  ->  https://pr-12.client.com
    index.html, 200.html, 404.html       static site (200.html = SPA fallback, 404.html = not found)
    run (executable)                     running app: a sandboxed service on $PORT, Caddy proxies to it
```

Two static Go binaries, no runtime dependencies:

- **`app-host`** (root, and the dashboard's own user): install, create/delete apps, sync, grants, env, invites, updates, the dashboard.
- **`app`** (everyone): `app site deploy|rm|restart|list|logs`. It only ever acts as whoever runs it.

## Install

On a fresh Debian 13 VPS, as root:

```sh
curl -fsSL https://raw.githubusercontent.com/sparkstonepdx/app-host/main/setup-server.sh | sh   # public repo
# or, while private: git clone the repo, cd into it, sh setup-server.sh  (builds from source)
```

That installs Caddy, swap, both binaries, and runs `app-host install`, which prints a one-time setup link: `https://<server ip>:8443/invite?...` (self-signed; accept the warning). Create your login, then on the Domains page set the dashboard's address and grant domains to apps. Open ports 22, 80, 443 (tcp + udp) and 8443.

Copying the two binaries onto a box and running `./app-host install` does the same, minus swap.

## Updating

- Releases: push a tag like `v1.2.3`; the release workflow builds both binaries for amd64 and arm64, signs `checksums.txt` with ed25519, and publishes a GitHub release. One-time setup: `scripts/setup-release-key.sh`.
- Servers: `app-host update` verifies the signature against the key built into the running binary, swaps both binaries, runs the install step again, and restarts the dashboard. Running sites are not touched.
- Notifications: a daily check shows "update available" on the dashboard (with an Update button) and in the SSH login message, and posts to `UPDATE_NOTIFY_URL` (ntfy, Discord or Slack) if set in `/etc/apps/update.env`.
- While the repo is private the server needs a read-only `GITHUB_TOKEN` in `/etc/apps/update.env`.

## Dashboard

![Apps](docs/dashboard-apps.png)

Apps and sites (state, port, memory, live or not), create/delete, sync, restart/stop/start, remove sites, logs, environment, domains, users, updates.

- **Split in two:** `app-host web` serves the UI and handles logins as the unprivileged `app-host-web` user, in a tight systemd sandbox. Everything else goes over a unix socket to `app-host helper` (root), which accepts a fixed list of operations and checks the caller with `SO_PEERCRED`. A bug in the web side doesn't hand out a root shell.
- **Logins:** per user, scrypt-hashed; HMAC-signed session cookies (30 days). Invites are signed one-time links (24 h). Removing a user or changing a password logs their sessions out.
- **Requests:** JSON only, from the dashboard's own origin; strict CSP; failed logins throttled per client IP (5 free, then up to 60 s).
- **Reaching it:** always at `https://<ip>:8443` (self-signed), plus the address set on the Domains page.
- **UI source:** `dashboard/ui` (Solid + `@sparkstone/css`), built into `dashboard/public` and embedded in the binary. Change it with `cd dashboard/ui && npm install && npm run dev` (proxies `/api` to a dashboard on :19999), then `npm run build` and commit `dashboard/public`. CI fails if it's stale.

## Deploying from GitHub

`app create` prints everything a repo needs: one secret and one workflow step.

```sh
gh secret set APP_HOST -R sparkstonepdx/myapp     # paste the apphost1:... value when it asks
```

```yaml
- uses: sparkstonepdx/app-host/deploy@v1
  with:
    app-host: ${{ secrets.APP_HOST }}
    dir: dist          # an executable "run" inside makes it a running app
    site: myapp        # or a hostname like client.com (needs a grant)
```

- Pushes deploy to `site`. Pull requests deploy a preview (`myapp-pr-12`, or `pr-12.client.com` for a hostname), comment its URL on the PR, and remove it when the PR closes. Turn that off with `previews: "false"`.
- The job fails with a clear message if the site didn't go live (not granted, reserved, or taken).
- Outputs: `url`, `site`. The workflow needs `permissions: pull-requests: write` for the PR comment.
- Pin `@v1` (moved on each release) or a commit SHA, never `@main`.
- While this repo is private, turn on **Settings → Actions → General → Access → "Accessible from repositories in the sparkstonepdx organization"** once.
- `APP_HOST` is `apphost1:` + base64url JSON (host, port, user, app folder, default domain, known_hosts, deploy key). The action pins the server's host key; it never disables host key checking.

## Runtime environment

![Environment](docs/dashboard-env.png)

Env vars and secrets for running apps live on the server in `/etc/apps/env/NAME.env` (root-only), not in GitHub. A `secrets.env` the app's own deploys write into its folder still works: `app sync` copies it, reading it as the app user, and `app-host env` values override it. Set them on the dashboard (app → Environment, including pasting a .env block) or with `app-host env`. Saving restarts the app's running sites. `PORT`, `HOST`, `SITE`, `SITE_HOST` are set by app-host and can't be overridden.

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

## Catch-all page

Any hostname that points here but isn't a site (or the dashboard) gets a plain 404 page on `http://`. Known sites still redirect to HTTPS, and `http://<ip>` redirects to the dashboard on `:8443`. Edit the page at `/etc/caddy/catchall/index.html`; `app-host install` never overwrites it, and removes Debian's stock `:80` welcome-page block from `/etc/caddy/Caddyfile` (keeping a backup), because two `:80` sites can't coexist.

Over `https://`, an unknown hostname fails the TLS handshake instead, because Caddy only has certificates for names it serves.

## Commands

Root (`app-host`):

```
app-host install [--domain D] [--ip IP]
app-host create NAME [--memory 256M] [--cpu 50%] [--tasks 256] [--repo owner/name]
app-host delete NAME
app-host sync NAME | --all
app-host grant list [APP] | grant add|rm APP domain|sub|host NAME
app-host env list|set|unset NAME ...
app-host invite
app-host update [--check]
```

App users, and deploy keys through the SSH gate (`app`):

```
app site deploy DIR SITE    # atomic swap into sites/SITE; exits 2 if SITE didn't go live
app site rm SITE            # SITE: "blog" (subdomain of the default domain) or "client.com" (exact)
app site restart SITE
app site list
app site logs SITE [N]
```

## What each app gets

| Path | Purpose |
|---|---|
| `/srv/apps/<app>/sites/` | one folder per hostname |
| `/srv/apps/<app>/builds/` | upload area for rsync |
| `/srv/apps/<app>/data/` | persistent data for running apps (survives redeploys) |
| `/srv/apps/<app>/secrets.env` | optional env vars written by the app's own deploys; `app-host env` values override them |
| `/var/log/apps/<app>/<site>.log` | output of running sites (root-owned, app-readable, rotated hourly at 10 MB) |

Running sites listen on `127.0.0.1:$PORT` and also get `$SITE`, `$SITE_HOST`, `$HOME`. Ports come from 20000–29999. Everything one app runs shares a systemd slice (default 256M memory, 50% CPU, 256 tasks; set per app at creation).

## Security model

- **App folders are `700`.** Static sites are served from a root-owned copy in `/var/lib/app-host/www/`, made by `app` running as the app user (so symlinks only reach what that app could already read); only plain files and folders are unpacked.
- **Root never opens a path inside an app's folder by name.** Directory reads use `O_NOFOLLOW` file descriptors and `*at()` calls; reading an app's own files happens as the app user. Env files, logs and deploy keys live in root-owned places.
- **Deploy keys can only deploy.** sshd forces every `app-*` login through `app ssh-gate`, which allows an rsync upload into `builds/<name>/` and `app site …`: no shell, no port forwarding, no downloads. Keys live in `/etc/ssh/app-host-keys/`, which apps can't write.
- **Running sites are sandboxed.** `ProtectSystem=strict`, only their own folder visible, no new privileges, no capabilities, may only listen on their own `$PORT`, single files capped at 2 GB.
- **Caddy's admin API is on a root-only unix socket**, not `localhost:2019`, so apps can't rewrite Caddy's config.
- **Updates are signed:** the binary refuses releases whose signature doesn't match its built-in key.
- **Known limits:** no per-app disk quota for `data/` (the 2 GB file cap and hourly log rotation help); the first visit to the IP dashboard trusts a self-signed certificate.

## Tested

Go unit tests cover names, grants, the Caddy config, scrypt (RFC 7914 vectors and Node's output), env files, the SSH gate, tar extraction and update verification. In a sandbox with real users, Caddy, sshd and rsync, and a stand-in for systemd: migration from the bash/Node version, login with the old password hashes, the dashboard API end to end, the deploy action through the SSH gate (push, PR preview, close, refused host), gate escapes refused, and the earlier exploits (symlinked site files, swapped `secrets.env`, symlinked `sites/`, Caddy admin access) blocked. Not tested: real systemd and a real GitHub runner.
