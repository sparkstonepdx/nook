---
"nook": minor
---

A web site with `"tls": true` gets Caddy's certificate for its hostname as systemd credentials (`tls.crt`, `tls.key` in `$CREDENTIALS_DIRECTORY`), for ports it serves TLS on itself, like a mail server's 465, 587 and 993. Renewals restart it within 2 minutes (#95 step 3).
