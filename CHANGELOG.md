# nook

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
