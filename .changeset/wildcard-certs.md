---
"nook": minor
---

Wildcard certificates from the Domains page. Every app and PR preview used to get its own certificate, and Let's Encrypt allows 50 a week per domain. Domains → Certificates sets up `*.domain` by DNS challenge, with Cloudflare first. It adds Caddy's DNS plugin and holds the package, checks the API token can see the zone, keeps the token root-only and hands it to Caddy through a systemd drop-in, and turns on `auto_https prefer_wildcard` so each app's hostnames use the wildcard. It shows each certificate's status and can update Caddy keeping its plugins. Also `nook-admin certs`, and `nook-admin headroom 7d` now honours its range.
