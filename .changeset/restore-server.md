---
"nook": minor
---

Restore a whole server from its backups (#68 part 2): on a fresh install, `nook-admin backup setup --existing` then `nook-admin backup restore-server`. Settings, users, certificates, deploy keys, each app's user (same uid where free) and folder come back, wildcard certificates are reapplied, and every app syncs. The new server takes over the old one's id unless `--new-id`.
