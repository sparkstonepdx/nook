---
"nook": patch
---

Turning on a wildcard works with current Caddy. nook added `auto_https prefer_wildcard` to the Caddyfile, but Caddy removed that option once it started preferring a managed wildcard by itself, and current Caddy refuses the whole config over it. nook no longer writes it, and takes it out if an earlier version did.
