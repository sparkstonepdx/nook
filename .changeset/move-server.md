---
"nook": minor
---

Move a server onto a new one (#68 part 3): `nook-admin move code` on the old server, `nook-admin move from CODE` on a fresh install. The new server takes the old one's backup settings, has it stop its apps and back up once more, restores that as a whole-server restore, and starts everything; `nook-admin move resume` on the old server undoes the stop.
