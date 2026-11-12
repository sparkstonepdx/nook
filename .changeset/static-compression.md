---
"nook": minor
---

Static sites are served compressed: text files get Brotli (`.br`) and gzip (`.gz`) copies when published, and Caddy sends Brotli, else gzip, to browsers that accept them.
