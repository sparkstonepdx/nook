---
"nook": minor
---

The firewall also keeps other apps off the ports an app listens on outside the app range (OpenCloud's services on 9200 and the like): only the app itself, root and Caddy may connect. nook reads them from the kernel every 30 seconds and reloads the rules when they change.
