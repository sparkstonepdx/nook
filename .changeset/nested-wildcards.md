---
"nook": minor
---

Sites more than one level under a domain with a wildcard get one too. `a.b.example.com` gets `*.b.example.com`, shared by everything at that level, through the same DNS provider and token. These are worked out from the published sites on every sync, so one goes away with its last site. Each site keeps its own certificate until its wildcard is issued. The Domains page lists them under their domain, with each one's status.
