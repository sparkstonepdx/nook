---
"nook": patch
---

The launcher's tiles show what Android shows for an installed PWA: the manifest's maskable icon filling the tile, else the site's app icon at 80% on its background color, else its favicon (the page's `rel="icon"`, `/favicon.svg` or `/favicon.ico`) at 60%, else its initial. Cards carry `maskable` and `favicon` beside `icon`; the Directory app still shows `icon`.
