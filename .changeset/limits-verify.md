---
"nook": patch
---

Changing an app's limits now checks that the running slice took them. If systemd refused, or something with more say kept the old value, the dashboard says so with systemd's reason and the slice's drop-ins, instead of saying it changed.
