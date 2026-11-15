---
"nook": minor
---

Store installs and updates show their progress, like nook's own updates: the steps (download with MB and a bar, hash check, unpack, deploy, health check, and rollback if it comes to that), in the install or update sheet and as "Updating · 45%" on the Installed list. The sheet can be hidden; the job keeps going and the list keeps showing it. Updates the daily check starts on its own show up too. `GET /api/store/jobs` has the current jobs.

Fix: a store install or update whose download or hash check failed crashed nook-helper (v1.25.1) instead of reporting why. Scratch folders a crash left in `/var/lib/nook` are swept when the helper starts.
