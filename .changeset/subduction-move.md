---
"nook": minor
---

One-off: the Apps page offers to move a Subduction set up by hand (extras/subduction-app.sh) onto the catalog's Subduction. It stops the old one, installs the catalog app as subduction-2 on the same site with the old data and key copied in, and puts the old one back if any step fails. Then it waits: keep the new one (the old app is deleted) or go back. The Subduction identity card follows whichever app serves the subduction site. To be removed once the move is done.

The Subduction contact card no longer needs Node on the server: when no card is saved, nook makes it from the key in Go (an X25519 share key derived from the key, signed with it the way Keyhive does), the same card every time. A saved card is still used as before.

The Subduction panel (address, peer id, contact card) shows in the Manage sheet of any Subduction app: the catalog's under whatever app and site name it was installed with, or the hand setup's. Its address is that app's own site. `GET /api/subduction?app=NAME` reads one; without `app` it's the first Subduction on the server, as `nook-admin subduction` prints.
