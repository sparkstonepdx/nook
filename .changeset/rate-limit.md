---
"nook": patch
---

When Let's Encrypt's weekly limit is reached, the wildcard goes first: hosts that don't have a certificate yet stop asking for their own and wait for it, while hosts that have one keep it. The Domains page (and `nook-admin certs`) shows "rate limited" and when the next try can work, in your own time. Before, the retry time was cut off and new hosts used up the limit as it lifted.
