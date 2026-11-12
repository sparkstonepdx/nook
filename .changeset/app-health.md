---
"nook": minor
---

Apps can declare a health path in nook.json (`"health": "/healthz"`). Deploys wait for it and fail the job if it does not answer, and the dashboard shows unhealthy sites with the reason.
