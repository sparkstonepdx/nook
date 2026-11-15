---
"nook": patch
---

Fix: **Check for updates** now covers the Store too. It fetches every catalog fresh and says which installed apps have a newer version, with a link to the Store, instead of only checking nook itself. Before, a new catalog release (like Directory 0.2.0) didn't show until the catalog's hourly cache ran out or you pressed Refresh catalog on the Store page.
