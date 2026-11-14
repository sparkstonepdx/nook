---
"nook": patch
---

Turning on a wildcard no longer takes sites offline while it's pending. Caddy 2.10+ stops getting certificates for each hostname once a managed wildcard covers them, so a wildcard held up by a rate limit left every subdomain, the dashboard included, with no certificate. Now each hostname under a wildcard keeps its own certificate (`tls force_automate`) until the wildcard is issued. The helper checks every 2 minutes and moves them over once it exists. The dashboard address always keeps its own.
