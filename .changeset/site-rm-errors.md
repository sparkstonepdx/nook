---
"nook": patch
---

Removing a site says when it didn't work. `nook site rm` used to report "removed" even when files were left behind; now it makes read-only folders writable first, checks the site is gone, and otherwise shows what's in the way and how to remove it as root.
