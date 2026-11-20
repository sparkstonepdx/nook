---
"nook": patch
---

catalog-build checks repos.json: each repository's fields, that its key file holds exactly the pinned keys, and that it answers for Debian 13, so a typo fails the catalog's CI instead of the first install.
