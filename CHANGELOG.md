# nook

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
