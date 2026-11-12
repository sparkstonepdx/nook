---
"nook": minor
---

Removes the built-in Postgres provider (`nook-admin services install postgres` and `drop-db`). It wasn't in use. `postgres` stays a known service: apps that need it get `DATABASE_URL` from whichever provider app it's bound to. Postgres from the catalog is proposed in #42.
