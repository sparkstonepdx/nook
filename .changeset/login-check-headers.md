---
"nook": patch
---

Fix: behind the Nook login, a request with a deploy token (or a directory card read) no longer reaches the app with `X-Nook-User` set to Caddy's placeholder text (`{http.reverse_proxy.header.X-Nook-User}`). nook-web now answers with both `X-Nook-App` and `X-Nook-User`; the one that doesn't apply is empty, so apps should treat empty as absent.
