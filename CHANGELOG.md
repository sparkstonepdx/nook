# nook

## 1.19.3

### Patch Changes

- 3cb2686: No more browser alert boxes. Remove site, and every other remove, revoke, unbind or change that used one, now asks in place: the button turns into the question with Cancel and a plain button in red text to confirm. Cancel has focus, so a double tap can't confirm by accident, and Escape backs out without closing the sheet.

## 1.19.2

### Patch Changes

- c96e4b7: Remove site, Restart, Stop and Start work from the dashboard again. The site sheet closed itself before building the request, which cleared the app's name, so every one went to `/api/apps//sites/…` and failed with "not found".

## 1.19.1

### Patch Changes

- 45d1cb8: Updating nook happens in one place. The banner and the footer both open an update sheet with the release notes and an Install button; there's no browser confirm. While nook restarts, the sheet says so instead of showing the cut-off connection as an error. It waits until the new version answers, then reloads once and says "Updated to nook v…". A failed download or signature check shows its output, and Check now opens the sheet when it finds an update.

## 1.19.0

### Minor Changes

- b3486d2: Sites more than one level under a domain with a wildcard get one too. `a.b.example.com` gets `*.b.example.com`, shared by everything at that level, through the same DNS provider and token. These are worked out from the published sites on every sync, so one goes away with its last site. Each site keeps its own certificate until its wildcard is issued. The Domains page lists them under their domain, with each one's status.

## 1.18.4

### Patch Changes

- 0e817cf: Turning on a wildcard no longer takes sites offline while it's pending. Caddy 2.10+ stops getting certificates for each hostname once a managed wildcard covers them, so a wildcard held up by a rate limit left every subdomain, the dashboard included, with no certificate. Now each hostname under a wildcard keeps its own certificate (`tls force_automate`) until the wildcard is issued. The helper checks every 2 minutes and moves them over once it exists. The dashboard address always keeps its own.

## 1.18.3

### Patch Changes

- a898621: Turning on a wildcard works with current Caddy. nook added `auto_https prefer_wildcard` to the Caddyfile, but Caddy removed that option once it started preferring a managed wildcard by itself, and current Caddy refuses the whole config over it. nook no longer writes it, and takes it out if an earlier version did.

## 1.18.2

### Patch Changes

- 553567c: The wildcard sheet says how to make the Cloudflare token, step by step, with a link to Cloudflare's API Tokens page.

## 1.18.1

### Patch Changes

- 9989cdf: Adding Caddy's DNS plugin works with Debian's Caddy, which has no `add-package`. nook downloads the current Caddy with the plugin from caddyserver.com, checks it runs, lists the module and accepts the config, swaps it in (keeping the old one as `caddy.nook-old`) and holds the package. Update Caddy does the same with the same plugins. A failed command's result no longer shows the success title.

## 1.18.0

### Minor Changes

- d8be2c5: Wildcard certificates from the Domains page. Every app and PR preview used to get its own certificate, and Let's Encrypt allows 50 a week per domain. Domains → Certificates sets up `*.domain` by DNS challenge, with Cloudflare first. It adds Caddy's DNS plugin and holds the package, checks the API token can see the zone, keeps the token root-only and hands it to Caddy through a systemd drop-in, and turns on `auto_https prefer_wildcard` so each app's hostnames use the wildcard. It shows each certificate's status and can update Caddy keeping its plugins. Also `nook-admin certs`, and `nook-admin headroom 7d` now honours its range.

### Patch Changes

- 88a3808: An open app on the Apps page is easier to read: its sites are flat rows inside the same card instead of a card within a card, one line each unless there's more to say than "static", problems first, with Sync and Manage (and a short watcher warning) at the top.

## 1.17.3

### Patch Changes

- 948f185: The deploy action sets up Go with `actions/setup-go` v7.

## 1.17.2

### Patch Changes

- ebdeaaf: After an update, the dashboard really runs the new version. The update restarts the dashboard page's process and waits for it, the new binary's install step checks for and restarts anything still running the old one, and the daily check and the helper's start do too. The footer shows the version actually serving the page, and says when an installed update isn't running yet. Also: the installed app's offline copy is only ever the dashboard page, not a file opened directly.

## 1.17.1

### Patch Changes

- b1e8cf6: Removing a site says when it didn't work. `nook site rm` used to report "removed" even when files were left behind; now it makes read-only folders writable first, checks the site is gone, and otherwise shows what's in the way and how to remove it as root.

## 1.17.0

### Minor Changes

- ffdded3: Logs read better: each line of a tracing-style log (Subduction's) shows its time, level, where it's from, the message and its fields, with long ids cut short (hover for the whole one). Show all, warnings or errors; Raw shows the log as the server wrote it (remembered per browser); Copy copies what's shown, without colour codes.

### Patch Changes

- fb9e49f: The deploy action sets up Go with `actions/setup-go` v6, which runs on Node 24, so runners no longer warn that it targets Node 20.

## 1.16.0

### Minor Changes

- 9f627d9: The Apps page is one row per app: its sites, memory and a count of any problems, opening to its sites, Sync and Manage. Which apps are open is remembered per browser, and Expand all opens them all. The dashboard can be installed as an app on phones and desktops, asks to be from the second visit, and offers Reload when nook has updated while a page was open.

## 1.15.0

### Minor Changes

- 74b8577: The dashboard shows the Subduction server's Keyhive identity: its address, peer id and contact card, each with a Copy, and Copy all as env lines. Apps need these to add the server to their docs as a relay. `nook-admin subduction` prints the same. `extras/subduction-app.sh` now makes the card from the server's key when Node is installed; re-run it to get one.

## 1.14.1

### Patch Changes

- 9f7aa4f: nook has a logo: an arch with an app inside. It's in the dashboard header, the login and invite pages, and the favicon. The files are in `docs/logo/`.

## 1.14.0

### Minor Changes

- f6399c7: Removes the built-in Postgres provider (`nook-admin services install postgres` and `drop-db`). It wasn't in use. `postgres` stays a known service: apps that need it get `DATABASE_URL` from whichever provider app it's bound to. Postgres from the catalog is proposed in #42.

## 1.13.0

### Minor Changes

- b855021: Static sites follow a Netlify-style `_redirects` file: rewrites (200) and redirects (301, 302, 303, 307, 308), with `:placeholders`, `*` splats and forced rules.

## 1.12.0

### Minor Changes

- ff62da5: Store apps have their own page: screenshots, details, what they ask for, and links to the original project's homepage and source code. Catalog entries can list an `icon` and `screenshots` (PNG or JPEG in the catalog repo, up to 6, 1 MB each); `catalog-build` pins their hashes and adds the project's source link, and nook checks each image before the dashboard shows it. Also fixes the install sheet suggesting the name of an app that's already installed.

## 1.11.0

### Minor Changes

- cab6b5c: A server-wide 404 page for static sites without their own `404.html`, edited on the dashboard's Domains page.

## 1.10.0

### Minor Changes

- 92b615a: Static sites can opt in to CORS: `{"cors": true}` in the site's `nook.json` adds `Access-Control-Allow-Origin: *`.
- bd9f20b: Static sites are served gzip-compressed: text files get a `.gz` copy when published, and Caddy sends it to browsers that accept gzip.

## 1.9.1

### Patch Changes

- 6c62c06: Sheets stay open while the page refreshes in the background. Typing an app's name to delete it no longer closes the Manage sheet every 10 seconds.

## 1.9.0

### Minor Changes

- a059fc2: The dashboard's Server section shows headroom: memory, CPU and disk with a status for each, upgrade advice (add RAM, a faster CPU, more disk, or raise one app's limit), and charts for the last day, week or month. nook records a sample every 5 minutes and notifies you when the status changes. App limits can now be changed after creation from Manage → Limits or `nook-admin limits`, without restarting the app. Dashboard notices are now a line with a status dot instead of boxes.

## 1.8.0

### Minor Changes

- 3eb858f: Apps can declare a health path in nook.json (`"health": "/healthz"`). Deploys wait for it and fail the job if it does not answer, and the dashboard shows unhealthy sites with the reason.

## 1.7.0

### Minor Changes

- 53ff476: Store: catalog apps can ship as `.zip` archives (PocketBase does), and the health check after an install or update sends the site's hostname, as Caddy does, so apps that route by hostname pass it.

## 1.6.1

### Patch Changes

- b72519b: Trust the official catalog's signing key (sparkstonepdx/nook-catalog), so the Store lists its apps.
- 988d49f: Deleting an app that was only partly created or deleted removes what is left of it instead of failing with "no app named".
- 41f6bf1: Logs and command output show terminal colours (ANSI codes) as styled text instead of raw codes like [2m.
- bee5fcc: New deploy tokens read `nook:admin.example.com/…`, so the address is easy to see. Tokens from 1.6 (`nook_admin.example.com/…`) and older ones keep working.

## 1.6.0

### Minor Changes

- 0ffe0df: Store catalogs: the official catalog moves to sparkstonepdx/nook-catalog and shows a green Verified check, and you can add community catalogs on the Store page. Each community catalog's signing key is pinned when it's added, and its apps say which catalog they come from.
- b207301: Deploy tokens carry the dashboard's address (`nook_admin.example.com/…`), so the deploy action needs only the `NOOK_TOKEN` secret. Older tokens still work with the action's `url` input.

## 1.5.0

### Minor Changes

- 484f94d: A new look for the dashboard on @sparkstone/css 4: phone first, with the sections in a dock under your thumb, actions in sheets that slide up from the bottom, and notices instead of a dialog after every command.

## 1.4.0

### Minor Changes

- faf682c: Add the Store: install apps from a signed catalog, with per-app update policies, health checks and automatic rollback.

## 1.3.0

### Minor Changes

- b37d1d6: The dashboard shows the version it runs, when it last checked for updates (or why the check failed), and a Check now button; Update appears as soon as a newer release exists.
