---
"nook": minor
---

Runtimes (#42 step 3): a site of kind `runtime` is published root-owned to `/var/lib/nook/runtimes/APP/SITE` and never run or served. An app that needs it (`"needs": ["php@8.3"]`) gets its bin folder first on PATH and in `NOOK_RUNTIME_BIN`, and restarts when it's updated. Runtimes can link to binaries apt installed under /usr.
