---
"nook": patch
---

Launcher tiles and directory cards get their icons again: the dashboard's service could only open Unix sockets, so it couldn't read any site's page through Caddy at 127.0.0.1:443 ("address family not supported by protocol"). nook-web may now open TCP to this machine only (`IPAddressAllow=localhost`); the firewall still keeps it off app ports.
