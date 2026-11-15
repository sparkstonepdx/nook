---
"nook": patch
---

Memory stays flat on big deploys and installs. Publishing a static site streams it from the app's folder into the served copy instead of holding it in memory (a 980 MB site went from 1.6 GB of RAM to 16 MB). Gzip copies of text files are written file to file. Store installs and updates download, check, unpack and keep their rollback copy through files in `/var/lib/nook`, and a binary over 500 MB is refused instead of cut short. Reading Caddy's log for certificate errors and rate limits keeps only the matching lines.
