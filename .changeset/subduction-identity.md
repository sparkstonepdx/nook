---
"nook": minor
---

The dashboard shows the Subduction server's Keyhive identity: its address, peer id and contact card, each with a Copy, and Copy all as env lines. Apps need these to add the server to their docs as a relay. `nook-admin subduction` prints the same. `extras/subduction-app.sh` now makes the card from the server's key when Node is installed; re-run it to get one.
