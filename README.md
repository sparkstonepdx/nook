# nook

Run apps and static sites on one Debian box behind Caddy, deployed from GitHub. Each repo gets an isolated app (its own Linux user and folder); every folder in its `sites/` is a live site, named by its hostname.

```
/srv/nook/<app>/sites/blog/              ->  https://blog.<default domain>
/srv/nook/<app>/sites/client.com/        ->  https://client.com   (+ www.client.com redirecting to it)
/srv/nook/<app>/sites/pr-12.client.com/  ->  https://pr-12.client.com
    index.html, 200.html, 404.html       static site (200.html = SPA fallback, 404.html = not found)
    run (executable)                     running app: a sandboxed service on $PORT, Caddy proxies to it
```

Two static Go binaries, no runtime dependencies:

- **`nook-admin`** (root, and the dashboard's own user): install, create/delete apps, sync, grants, env, invites, updates, the dashboard.
- **`nook`** (everyone): `nook site deploy|rm|restart|list|logs`. It only ever acts as whoever runs it.

## Install

On a fresh Debian 13 VPS, as root:

```sh
curl -fsSL https://raw.githubusercontent.com/sparkstonepdx/nook/main/setup-server.sh | sh   # public repo
# or, while private: git clone the repo, cd into it, sh setup-server.sh  (builds from source)
```

That installs Caddy, swap, both binaries, and runs `nook-admin install`, which prints a one-time setup link: `https://<server ip>:8443/invite?...` (self-signed; accept the warning). Create your login, then on the Domains page set the dashboard's address and grant domains to apps. Open ports 22, 80, 443 (tcp + udp) and 8443.

Copying the two binaries onto a box and running `./nook-admin install` does the same, minus swap.

### Coming from app-host

This project used to be called app-host. Running `nook-admin install` (or `--upgrade`) on an app-host box moves it over in place: `app-NAME` users become `nook-NAME` (same uid, files untouched), `/srv/apps`, `/etc/apps`, `/var/log/apps`, `/var/lib/app-host*` and `/etc/ssh/app-host-keys` move to their nook names, the old units, sshd and Caddy config are removed, and every app is re-synced. Existing `APP_HOST` tokens and `ssh ... app site ...` still work, so repos keep deploying; rename the secret to `NOOK_TOKEN` and switch to `sparkstonepdx/nook/deploy@v1` with `token:` when convenient. Re-run `extras/subduction-app.sh` afterwards if you use it.

## Updating

- Releases: push a tag like `v1.2.3`; the release workflow builds both binaries for amd64 and arm64, signs `checksums.txt` with ed25519, and publishes a GitHub release. One-time setup: `scripts/setup-release-key.sh`.
- Servers: `nook-admin update` verifies the signature against the key built into the running binary, swaps both binaries, runs the install step again, and restarts the dashboard. Running sites are not touched.
- Notifications: a daily check shows "update available" on the dashboard (with an Update button) and in the SSH login message, and posts to `UPDATE_NOTIFY_URL` (ntfy, Discord or Slack) if set in `/etc/nook/update.env`.
- While the repo is private the server needs a read-only `GITHUB_TOKEN` in `/etc/nook/update.env`.

## Dashboard

![Apps](docs/dashboard-apps.png)

Apps and sites (state, port, memory, live or not), create/delete, sync, restart/stop/start, remove sites, logs, environment, domains, users, updates.

- **Split in two:** `nook-admin web` serves the UI and handles logins as the unprivileged `nook-web` user, in a tight systemd sandbox. Everything else goes over a unix socket to `nook-admin helper` (root), which accepts a fixed list of operations and checks the caller with `SO_PEERCRED`. A bug in the web side doesn't hand out a root shell.
- **Logins:** per user, scrypt-hashed; HMAC-signed session cookies (30 days). Invites are signed one-time links (24 h). Removing a user or changing a password logs their sessions out.
- **Requests:** JSON only, from the dashboard's own origin; strict CSP; failed logins throttled per client IP (5 free, then up to 60 s).
- **Reaching it:** always at `https://<ip>:8443` (self-signed), plus the address set on the Domains page.
- **UI source:** `dashboard/ui` (Solid + `@sparkstone/css`), built into `dashboard/public` and embedded in the binary. Change it with `cd dashboard/ui && npm install && npm run dev` (proxies `/api` to a dashboard on :19999), then `npm run build` and commit `dashboard/public`. CI fails if it's stale.

## Deploying from GitHub

`app create` prints everything a repo needs: one secret and one workflow step.

```sh
gh secret set NOOK_TOKEN -R sparkstonepdx/myapp     # paste the nook1:... value when it asks
```

```yaml
- uses: sparkstonepdx/nook/deploy@v1
  with:
    nook: ${{ secrets.NOOK_TOKEN }}
    dir: dist          # an executable "run" inside makes it a running app
    site: myapp        # or a hostname like client.com (needs a grant)
```

- Pushes deploy to `site`. Pull requests deploy a preview (`myapp-pr-12`, or `pr-12.client.com` for a hostname), comment its URL on the PR, and remove it when the PR closes. Turn that off with `previews: "false"`.
- The job fails with a clear message if the site didn't go live (not granted, reserved, or taken). It waits until the new build is the one being served, then until the site answers over HTTPS (a new hostname needs its certificate first; that part only warns).
- The action is a small Go program in this repo (`cmd/deploy-action`, standard library only), built on the runner. GitHub-hosted runners already have Go; on others it installs it with `actions/setup-go`. It needs `ssh` and `rsync` on the runner.
- Outputs: `url`, `site`. The workflow needs `permissions: pull-requests: write` for the PR comment.
- Pin `@v1` (moved on each release) or a commit SHA, never `@main`.
- While this repo is private, turn on **Settings → Actions → General → Access → "Accessible from repositories in the sparkstonepdx organization"** once.
- `NOOK_TOKEN` is `nook1:` + base64url JSON (host, port, user, app folder, default domain, known_hosts, deploy key). The action pins the server's host key; it never disables host key checking.

## Runtime environment

![Environment](docs/dashboard-env.png)

Env vars and secrets for running apps live on the server in `/etc/nook/env/NAME.env` (root-only), not in GitHub. A `secrets.env` the app's own deploys write into its folder still works: `app sync` copies it, reading it as the app user, and `nook-admin env` values override it. Set them on the dashboard (app → Environment, including pasting a .env block) or with `nook-admin env`. Saving restarts the app's running sites. `PORT`, `HOST`, `SITE`, `SITE_HOST` are set by nook-admin and can't be overridden.

## Domains

![Domains](docs/dashboard-domains.png)

- **Default domain** (`BASE_DOMAIN`): site folders without a dot are subdomains of it.
- **Grants**, per app, in `/etc/nook/grants`:
  - `domain client.com`: `client.com` and every `*.client.com`
  - `sub client.com`: `*.client.com` only (use this for a shared domain)
  - `host blog.client.com`: exactly that hostname

  New apps get `sub <default domain>`. Several apps can share a domain; the first to publish a hostname keeps it.
- A root domain an app owns (`domain` or `host` grant) with no `www.` folder gets `www.` redirecting to it. A real `www.` site in any app replaces the redirect.
- The Domains page lists the DNS records each grant needs.

## Catch-all page

Any hostname that points here but isn't a site (or the dashboard) gets a plain 404 page on `http://`. Known sites still redirect to HTTPS, and `http://<ip>` redirects to the dashboard on `:8443`. Edit the page at `/etc/caddy/catchall/index.html`; `nook-admin install` never overwrites it, and removes Debian's stock `:80` welcome-page block from `/etc/caddy/Caddyfile` (keeping a backup), because two `:80` sites can't coexist.

Over `https://`, an unknown hostname fails the TLS handshake instead, because Caddy only has certificates for names it serves.

## Commands

Root (`nook-admin`):

```
nook-admin install [--domain D] [--ip IP]
nook-admin create NAME [--memory 256M] [--cpu 50%] [--tasks 256] [--repo owner/name]
nook-admin delete NAME
nook-admin sync NAME | --all
nook-admin grant list [APP] | grant add|rm APP domain|sub|host NAME
nook-admin env list|set|unset NAME ...
nook-admin invite
nook-admin update [--check]
```

App users, and deploy keys through the SSH gate (`nook`):

```
nook site deploy DIR SITE    # atomic swap into sites/SITE; exits 2 if SITE didn't go live
nook site rm SITE            # SITE: "blog" (subdomain of the default domain) or "client.com" (exact)
nook site restart SITE
nook site list
nook site logs SITE [N]
```

## What each app gets

| Path | Purpose |
|---|---|
| `/srv/nook/<app>/sites/` | one folder per hostname |
| `/srv/nook/<app>/builds/` | upload area for rsync |
| `/srv/nook/<app>/data/` | persistent data for running apps (survives redeploys) |
| `/srv/nook/<app>/secrets.env` | optional env vars written by the app's own deploys; `nook-admin env` values override them |
| `/var/log/nook/<app>/<site>.log` | output of running sites (root-owned, app-readable, rotated hourly at 10 MB) |

Running sites listen on `127.0.0.1:$PORT` and also get `$SITE`, `$SITE_HOST`, `$HOME`. Ports come from 20000–29999. Everything one app runs shares a systemd slice (default 256M memory, 50% CPU, 256 tasks; set per app at creation).

## Security model

- **App folders are `700`.** Static sites are served from a root-owned copy in `/var/lib/nook/www/`, made by `nook` running as the app user (so symlinks only reach what that app could already read); only plain files and folders are unpacked.
- **Root never opens a path inside an app's folder by name.** Directory reads use `O_NOFOLLOW` file descriptors and `*at()` calls; reading an app's own files happens as the app user. Env files, logs and deploy keys live in root-owned places.
- **Deploy keys can only deploy.** sshd forces every `nook-*` login through `nook ssh-gate`, which allows an rsync upload into `builds/<name>/` and `nook site …`: no shell, no port forwarding, no downloads. Keys live in `/etc/ssh/nook-keys/`, which apps can't write.
- **Running sites are sandboxed.** `ProtectSystem=strict`, only their own folder visible, no new privileges, no capabilities, may only listen on their own `$PORT`, single files capped at 2 GB.
- **Caddy's admin API is on a root-only unix socket**, not `localhost:2019`, so apps can't rewrite Caddy's config.
- **Updates are signed:** the binary refuses releases whose signature doesn't match its built-in key.
- **Known limits:** no per-app disk quota for `data/` (the 2 GB file cap and hourly log rotation help); the first visit to the IP dashboard trusts a self-signed certificate.

## Tested

Go unit tests cover names, grants, the Caddy config, scrypt (RFC 7914 vectors and Node's output), env files, the SSH gate, tar extraction and update verification. In a sandbox with real users, Caddy, sshd and rsync, and a stand-in for systemd: migration from the bash/Node version, login with the old password hashes, the dashboard API end to end, the deploy action through the SSH gate (push, PR preview, close, refused host), gate escapes refused, and the earlier exploits (symlinked site files, swapped `secrets.env`, symlinked `sites/`, Caddy admin access) blocked. Not tested: real systemd and a real GitHub runner.
