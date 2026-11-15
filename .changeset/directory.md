---
"nook": minor
---

A launcher and a directory. The top of the Apps page shows every site as a tile with its own icon and name from its web manifest; one tap opens it. **List in directory** on a site puts it in a public list, `GET /api/v1/directory`, which the Directory app in the catalog shows to anyone. `nook-admin directory` lists and changes it, and catalog env defaults can use `{dashboard_url}`.
