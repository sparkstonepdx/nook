# nook

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
