---
"nook": minor
---

Backups (#68, part 1): daily, before each nook update and on demand, to an S3-compatible bucket with restic, encrypted. One snapshot of the server's setup and one per app; an app's `nook.json` `"backup"` can run a dump script first and leave folders out, an app without one is paused while it's read, and a whole app can be left out from its Manage sheet. The password is shown once and kept by nook. Each server's snapshots carry its id, so servers can share a bucket. Restoring an app's data is on its Manage sheet (`nook-admin backup restore`). A failed or missing backup is advice on the Server page and a push.
