# nook

## 1.43.0

### Minor Changes

- ffe6d08: Move an app's service to another provider with its data: `nook-admin migrate APP SERVICE TO`, or **Move** beside a binding on the Services page. The old provider's `export` hook streams into the new one's `import` hook while the app is stopped; on failure the app goes back to its old binding unchanged. With the catalog's Postgres hooks, this moves a database from postgres-16 to postgres-17 (#42 step 4).

## 1.42.1

### Patch Changes

- e2bd272: The store's app page says what an app provides (and which apps in your catalogs use it) and what it uses (required or optional, and which apps provide it), with links between them.

## 1.42.0

### Minor Changes

- b649281: Every app gets a soft memory line (`MemoryHigh`) at 90% of its memory limit: above it the kernel trims its cache and slows it instead of killing it. The Server section says when an app was squeezed that day. Existing apps get the line on update.

## 1.41.1

### Patch Changes

- bb7f594: Installing another copy of a store app (a second PocketBase) suggests the first free name for both the app and its site (pocketbase-2 at pocketbase-2.<domain>), and the install refuses a site another app already has, before making anything.

## 1.41.0

### Minor Changes

- a42c8e4: A web site can provide services too, not only a hostless service: for a provider that also has pages of its own, like Formlify's submissions.

## 1.40.0

### Minor Changes

- baa44e0: The firewall also keeps other apps off the ports an app listens on outside the app range (OpenCloud's services on 9200 and the like): only the app itself, root and Caddy may connect. nook reads them from the kernel every 30 seconds and reloads the rules when they change.

## 1.39.0

### Minor Changes

- f3f9734: Apt repositories in the catalog can be flat (`"suite": "./"`, no components), as Collabora's is, and can pin more than one key (`"keys"`) for a key file that holds a rotated key next to its successor. Every key in the file must be pinned, since apt trusts all of them for the repository.

## 1.38.0

### Minor Changes

- 50d539c: System updates from the dashboard: the Server section shows the server's waiting Debian updates and which are security fixes, installs them (all or security only, leaving store apps' apt packages to their own policy), restarts what still runs replaced libraries, says when a new kernel needs a restart and restarts the server after asking. A switch installs security fixes by themselves with the daily check. Also `nook-admin system`.

## 1.37.0

### Minor Changes

- dd1546e: Store: catalog entries can be runtimes (`"kind": "runtime"`, with `links` templated by `{version}` for apt) and can list public `ports`, which installing grants. `nook site deploy` and the deploy API report a runtime site as `(runtime)` instead of waiting for a hostname.

## 1.36.0

### Minor Changes

- 7d077f6: A running site or cron job can opt in to a syscall filter (#34) with `{"sandbox": {"syscalls": "strict"}}` in its nook.json: systemd's `@system-service` allowlist minus `@privileged` and `@resources`, blocked calls failing as EPERM. The dashboard marks it. Opt-in while it's tried app by app.

## 1.35.0

### Minor Changes

- 3ea03c0: Move a server onto a new one (#68 part 3): `nook-admin move code` on the old server, `nook-admin move from CODE` on a fresh install. The new server takes the old one's backup settings, has it stop its apps and back up once more, restores that as a whole-server restore, and starts everything; `nook-admin move resume` on the old server undoes the stop.

### Patch Changes

- 2e5a0ea: The site links to the app catalog (catalog.sparkstonepdx.com) from its header, footer, the store card and the store section.

## 1.34.0

### Minor Changes

- 6c58c4e: Public ports (#95): a running site can ask in its nook.json to listen on the server's own ports (`"ports": [25, 993]`), and an admin grants them on the Manage sheet or with `nook-admin ports APP grant 25,993`. Only that unit may bind them, with `CAP_NET_BIND_SERVICE` for ports under 1024; SSH, DNS, Caddy's ports and the app port range are never granted.

## 1.33.0

### Minor Changes

- 051438e: Runtimes (#42 step 3): a site of kind `runtime` is published root-owned to `/var/lib/nook/runtimes/APP/SITE` and never run or served. An app that needs it (`"needs": ["php@8.3"]`) gets its bin folder first on PATH and in `NOOK_RUNTIME_BIN`, and restarts when it's updated. Runtimes can link to binaries apt installed under /usr.

## 1.32.0

### Minor Changes

- 6364ec2: Services: a need can be optional (`"notify?"`), and static sites can list needs. A provider bound to a static site can work on it: a `publish` hook rewrites its HTML before it goes live, and `posts` sends its POST requests to the provider. The groundwork for a form catcher (#98).

## 1.31.0

### Minor Changes

- 9d58a3e: Store apps can come from apt (#42 step 2), official catalog only: an entry names Debian packages, optionally from a third-party repository the signed catalog defines once and pins by key fingerprint. nook masks the distro's own service and runs the program as an ordinary app. Entries with majors (`versions`) install each as its own app (`postgres-17`), and the install sheet picks the version. Providers' hooks get `PORT`.

## 1.30.0

### Minor Changes

- 3a0648e: Services get versions and provisioning (#42 step 1). A provider states `"version"`, and consumers can need `"postgres>=15"` or `"php@8.3"`: binding refuses a provider that doesn't meet it and auto-bind picks the newest that does. A provider's `"provision"` hook runs when an app is bound and prints the app's env (its own login and database); `"deprovision"` runs when the binding goes.

## 1.29.4

### Patch Changes

- 8654daa: The deploy action names the address it tried when a token is refused, and says so when nothing there answered as nook. The site workflow uses the NOOK_TOKEN secret.

## 1.29.3

### Patch Changes

- 25deb4b: A subdomain under a wildcard certificate with no site yet shows the server's 404 page instead of a blank screen: the wildcard's catch-all block answered an empty 404.

## 1.29.2

### Patch Changes

- df1a8ba: Launcher tiles and directory cards get their icons again: the dashboard's service could only open Unix sockets, so it couldn't read any site's page through Caddy at 127.0.0.1:443 ("address family not supported by protocol"). nook-web may now open TCP to this machine only (`IPAddressAllow=localhost`); the firewall still keeps it off app ports.

## 1.29.1

### Patch Changes

- 42a89d8: Deploys stop dropping every app's websockets. A redeploy no longer reloads Caddy: the app's Caddy file only changes in its "# site" lines, which Caddy ignores, so nook compares without them. On Caddy 2.7 and newer, every app's reverse proxy also gets `stream_close_delay 2m`, so the reloads that remain (a site added or removed, previews) leave live connections open; nook checks the version first, since older Caddy rejects the setting. Upgrading Caddy from the dashboard rewrites the apps' blocks to add it.

## 1.29.0

### Minor Changes

- 3daef98: Restore a whole server from its backups (#68 part 2): on a fresh install, `nook-admin backup setup --existing` then `nook-admin backup restore-server`. Settings, users, certificates, deploy keys, each app's user (same uid where free) and folder come back, wildcard certificates are reapplied, and every app syncs. The new server takes over the old one's id unless `--new-id`.

## 1.28.7

### Patch Changes

- 93897d9: Cleanup: one way to word sizes and times across the dashboard (`format.ts`: small disk use now reads "12 KB" instead of "under 1 MB"), one helper for per-browser settings (`storage.ts`), and Go's `cmp.Or` in place of three copies of `cmpOr`.

## 1.28.6

### Patch Changes

- 24fba18: The Apps list shows the 5 apps changed most recently, plus any app with a problem, with "Show all N apps" under them (remembered per browser). Each app reports when a site of it last changed (`changed` in /api/overview).

## 1.28.5

### Patch Changes

- 65721da: A launcher tile with no icon says why: the site's sheet (and the tile's tooltip) shows whether the page couldn't be read, had no manifest link, or listed icons that didn't load.

## 1.28.4

### Patch Changes

- 4c517bf: Backups and the Subduction relay settings are off the home page. Backups has its own page in the nav; the relay's address, peer id and contact card are in the subduction app's Manage sheet. The phone dock fits six sections.

## 1.28.3

### Patch Changes

- d13a832: The launcher's tiles show what Android shows for an installed PWA: the manifest's maskable icon filling the tile, else the site's app icon at 80% on its background color, else its favicon (the page's `rel="icon"`, `/favicon.svg` or `/favicon.ico`) at 60%, else its initial. Cards carry `maskable` and `favicon` beside `icon`; the Directory app still shows `icon`.

## 1.28.2

### Patch Changes

- 9fa6d82: The Server section on the Apps page has room above it: a larger gap and a hairline after the apps list.

## 1.28.1

### Patch Changes

- 6e997db: Limits take memory the way people type it ("2g", "2 GB", "512mb", "1.5G") and CPU as a bare number ("50" is 50%). App disk is measured every 15 minutes instead of hourly, and opening an app on the Apps page measures it on the spot, so deleted files show right away.

## 1.28.0

### Minor Changes

- 5faab53: Backups (#68, part 1): daily, before each nook update and on demand, to an S3-compatible bucket with restic, encrypted. One snapshot of the server's setup and one per app; an app's `nook.json` `"backup"` can run a dump script first and leave folders out, an app without one is paused while it's read, and a whole app can be left out from its Manage sheet. The password is shown once and kept by nook. Each server's snapshots carry its id, so servers can share a bucket. Restoring an app's data is on its Manage sheet (`nook-admin backup restore`). A failed or missing backup is advice on the Server page and a push.

## 1.27.1

### Patch Changes

- c65b3a5: The launcher's tiles show each site's favicon (what its browser tab shows: the page's `rel="icon"`, else `/favicon.svg` or `/favicon.ico`), at 60% of the tile on its plain background. A site with no favicon keeps its app icon, then its initial. Cards carry it as `favicon`; the Directory app still shows the app icon.

## 1.27.0

### Minor Changes

- b662863: Per-app disk, and each app's charts in its row. nook measures every app's disk hourly (sites and served copies, builds, data, logs, store rollback copies) and trends it like memory: the Apps page row shows the size, a week's chart and when the disk would be full at that rate, with advice under 30 days. An app's memory and disk charts are now inside its expanded row on the Apps page, instead of a list in the Server section.

  Launcher tiles and directory cards get more icons: a site that doesn't answer `/` with a page (a token-only API) still gets its favicon, `/favicon.svg` and `/apple-touch-icon.png` are tried, a single-page app's HTML answer to `/favicon.ico` is skipped, and a store app with no icon of its own gets its catalog icon. Tiles with an icon come before letter tiles. Cards read Open Graph and Twitter tags: the share image (`image` in the directory list), and a description or name the page lacks.

  Fix: deploy API uploads and store installs left a full copy of every deploy in the app's `builds/` folder, doubling the disk each such site used. They now move the upload into place (`nook site deploy --move`). Existing leftovers in `/srv/nook/APP/builds/` from API deploys can be deleted; the Apps page shows them as "builds".

## 1.26.1

### Patch Changes

- 1d0bec9: Code moved into smaller files (nook-admin's commands, the store, folder reading, the dashboard's update parts); nothing changes for apps or the dashboard.

## 1.26.0

### Minor Changes

- 8b0305c: Store installs and updates show their progress, like nook's own updates: the steps (download with MB and a bar, hash check, unpack, deploy, health check, and rollback if it comes to that), in the install or update sheet and as "Updating · 45%" on the Installed list. The sheet can be hidden; the job keeps going and the list keeps showing it. Updates the daily check starts on its own show up too. `GET /api/store/jobs` has the current jobs.

  Fix: a store install or update whose download or hash check failed crashed nook-helper (v1.25.1) instead of reporting why. Scratch folders a crash left in `/var/lib/nook` are swept when the helper starts.

## 1.25.1

### Patch Changes

- 6718f4a: Memory stays flat on big deploys and installs. Publishing a static site streams it from the app's folder into the served copy instead of holding it in memory (a 980 MB site went from 1.6 GB of RAM to 16 MB). Gzip copies of text files are written file to file. Store installs and updates download, check, unpack and keep their rollback copy through files in `/var/lib/nook`, and a binary over 500 MB is refused instead of cut short. Reading Caddy's log for certificate errors and rate limits keeps only the matching lines.

## 1.25.0

### Minor Changes

- 3c9e075: Apps can ask for the nook login themselves: `{"auth": "nook-login"}` in a site's `nook.json` or a catalog entry puts it behind the dashboard login (or a deploy token, for scripts), with `"open"` paths still public. The app's login switch shows on and locked. Without a dashboard address the site is token-only, never open. For the Files store app, whose downloads now need a login.

## 1.24.1

### Patch Changes

- 13e34f6: Fix: behind the Nook login, a request with a deploy token (or a directory card read) no longer reaches the app with `X-Nook-User` set to Caddy's placeholder text (`{http.reverse_proxy.header.X-Nook-User}`). nook-web now answers with both `X-Nook-App` and `X-Nook-User`; the one that doesn't apply is empty, so apps should treat empty as absent.

## 1.24.0

### Minor Changes

- ccd5b66: Nook login for apps: an app's new Nook login switch puts all its sites behind the dashboard's login, with deploy tokens still working for scripts. Users can now be invited for only some apps: they get a launcher of those apps' sites instead of the dashboard.

## 1.23.0

### Minor Changes

- 8d73500: A launcher and a directory. The top of the Apps page shows every site as a tile with its own icon and name from its web manifest; one tap opens it. **List in directory** on a site puts it in a public list, `GET /api/v1/directory`, which the Directory app in the catalog shows to anyone. `nook-admin directory` lists and changes it, and catalog env defaults can use `{dashboard_url}`.

## 1.22.0

### Minor Changes

- 47f7d67: `{"listen": "any"}` in `nook.json` (and `listen` in catalog entries) lets a running site listen on any unprivileged port outside nook's app range, as well as its own. For CI runners, whose jobs start test servers.

## 1.21.1

### Patch Changes

- 1e27eb3: `catalog-build`: `source.tagPrefix` picks the newest release whose tag starts with it, so one repo can release several catalog apps (`push-v0.1.0`, `registry-v0.1.0`). The version is the tag without the prefix.

## 1.21.0

### Minor Changes

- e40ac0b: Each app's memory over time, with a projection. The Server section lists every app with its memory, CPU and a small chart, and says when an app that keeps growing will reach its limit (or fill the server, if it has none). An app heading there within 2 weeks gets advice and changes the server's status, so a slow leak shows up days ahead. `nook-admin headroom` prints the same list.

## 1.20.0

### Minor Changes

- 189ff5e: Token-only sites: a web site with `{"auth": "nook-token"}` in its `nook.json` only lets in requests that carry a deploy token from this server, sent as a Bearer token or as a Basic auth password (`docker login`). The app never sees the token. It gets `X-Nook-App`, the app the token belongs to. For private registries and APIs used by your own apps.

  `"open": ["/v1/health", "/v1/send/*"]` lists paths that skip the check, for callers like browsers that can't hold a token.

  Catalog entries can set `"auth": "nook-token"` and `"open"` too, so a store app can be token-only.

## 1.19.5

### Patch Changes

- 6e44baa: When Let's Encrypt's weekly limit is reached, the wildcard goes first: hosts that don't have a certificate yet stop asking for their own and wait for it, while hosts that have one keep it. The Domains page (and `nook-admin certs`) shows "rate limited" and when the next try can work, in your own time. Before, the retry time was cut off and new hosts used up the limit as it lifted.

## 1.19.4

### Patch Changes

- 5258250: The Services page no longer throws in the browser when a binding has no env.

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
