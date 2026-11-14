---
"nook": patch
---

Remove site, Restart, Stop and Start work from the dashboard again. The site sheet closed itself before building the request, which cleared the app's name, so every one went to `/api/apps//sites/…` and failed with "not found".
