---
"nook": minor
---

Per-app disk, and each app's charts in its row. nook measures every app's disk hourly (sites and served copies, builds, data, logs, store rollback copies) and trends it like memory: the Apps page row shows the size, a week's chart and when the disk would be full at that rate, with advice under 30 days. An app's memory and disk charts are now inside its expanded row on the Apps page, instead of a list in the Server section.

Launcher tiles and directory cards get more icons: a site that doesn't answer `/` with a page (a token-only API) still gets its favicon, `/favicon.svg` and `/apple-touch-icon.png` are tried, a single-page app's HTML answer to `/favicon.ico` is skipped, and a store app with no icon of its own gets its catalog icon. Tiles with an icon come before letter tiles. Cards read Open Graph and Twitter tags: the share image (`image` in the directory list), and a description or name the page lacks.

Fix: deploy API uploads and store installs left a full copy of every deploy in the app's `builds/` folder, doubling the disk each such site used. They now move the upload into place (`nook site deploy --move`). Existing leftovers in `/srv/nook/APP/builds/` from API deploys can be deleted; the Apps page shows them as "builds".
