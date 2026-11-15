---
"nook": minor
---

Apps can ask for the nook login themselves: `{"auth": "nook-login"}` in a site's `nook.json` or a catalog entry puts it behind the dashboard login (or a deploy token, for scripts), with `"open"` paths still public. The app's login switch shows on and locked. Without a dashboard address the site is token-only, never open. For the Files store app, whose downloads now need a login.
