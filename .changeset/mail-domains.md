---
"nook": minor
---

Mail domains (#95 step 2): a `mail client.com` grant lets an app handle mail for a domain without serving a website there. It gets its domains in `NOOK_MAIL_DOMAINS`, and its `"dns": "./dns.sh"` prints the MX, SPF, DKIM and DMARC records they need, which the Domains page lists with the rest. DNS records are now a list with a Copy button on each.
