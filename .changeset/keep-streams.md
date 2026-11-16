---
"nook": patch
---

Deploys stop dropping every app's websockets. A redeploy no longer reloads Caddy: the app's Caddy file only changes in its "# site" lines, which Caddy ignores, so nook compares without them. On Caddy 2.7 and newer, every app's reverse proxy also gets `stream_close_delay 24h`, so the reloads that remain (a site added or removed, previews) leave live connections open; nook checks the version first, since older Caddy rejects the setting. Upgrading Caddy from the dashboard rewrites the apps' blocks to add it.
