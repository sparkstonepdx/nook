---
"nook": patch
---

Update checks and downloads no longer fail with a bare 403. A GITHUB_TOKEN in update.env that GitHub refuses (expired, revoked, no access) is skipped and the request goes without it: the nook repo and the catalog are public. A used-up rate limit says so, with when it resets and that a token raises it; any other refusal shows GitHub's own message.
