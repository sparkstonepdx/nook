---
"nook": minor
---

nook has no Subduction code left. The hand-set-up Subduction and the one-off move are gone: the Apps page card, `/api/subduction/move`, the store install `prepare` hook, `extras/subduction-app.sh`, `extras/subduction-card.mjs`, and `SUBDUCTION=1` in setup-server.sh (it also built `automerge-subduction-ingest`). The identity panel in the Manage sheet, `GET /api/subduction` and `nook-admin subduction` are gone too: the catalog's Subduction app shows its address, peer id and Keyhive contact card on its own site.
