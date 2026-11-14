---
"nook": minor
---

Token-only sites: a web site with `{"auth": "nook-token"}` in its `nook.json` only lets in requests that carry a deploy token from this server, sent as a Bearer token or as a Basic auth password (`docker login`). The app never sees the token. It gets `X-Nook-App`, the app the token belongs to. For private registries and APIs used by your own apps.

`"open": ["/v1/health", "/v1/send/*"]` lists paths that skip the check, for callers like browsers that can't hold a token.

Catalog entries can set `"auth": "nook-token"` and `"open"` too, so a store app can be token-only.
