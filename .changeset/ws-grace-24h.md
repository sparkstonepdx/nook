---
"nook": patch
---

Live websockets outlast a Caddy reload for 24 hours instead of 2 minutes (`stream_close_delay 24h`), so long-lived sync connections survive deploys. Updating nook rewrites every app's block. A connection open before an access change (login turned on, a site removed) can now last up to a day after it.
