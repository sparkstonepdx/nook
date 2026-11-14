---
"nook": patch
---

`catalog-build`: `source.tagPrefix` picks the newest release whose tag starts with it, so one repo can release several catalog apps (`push-v0.1.0`, `registry-v0.1.0`). The version is the tag without the prefix.
