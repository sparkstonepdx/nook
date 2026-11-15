---
"nook": minor
---

`{"listen": "any"}` in `nook.json` (and `listen` in catalog entries) lets a running site listen on any unprivileged port outside nook's app range, as well as its own. For CI runners, whose jobs start test servers.
