---
"nook": minor
---

Public ports for a domain or a hostname (#95 step 2): `port-domain client.com 25,465,587,993` and `port-host mqtt.client.com 8883` grants, from the Domains page. Neither serves a website. A port domain's records (MX, SPF, DKIM, DMARC for mail) come from the app's `"dns": "./dns.sh"` and show on the Domains page; a port host gets an A record and a certificate the app's ports load (`HOST.crt` in `$CREDENTIALS_DIRECTORY`). Apps get them in `NOOK_PORT_DOMAINS` and `NOOK_PORT_HOSTS`. DNS records are now a list with a Copy button on each.
