---
"nook": patch
---

Adding Caddy's DNS plugin works with Debian's Caddy, which has no `add-package`. nook downloads the current Caddy with the plugin from caddyserver.com, checks it runs, lists the module and accepts the config, swaps it in (keeping the old one as `caddy.nook-old`) and holds the package. Update Caddy does the same with the same plugins. A failed command's result no longer shows the success title.
