---
"nook": patch
---

After an update, the dashboard really runs the new version. The update restarts the dashboard page's process and waits for it, the new binary's install step checks for and restarts anything still running the old one, and the daily check and the helper's start do too. The footer shows the version actually serving the page, and says when an installed update isn't running yet. Also: the installed app's offline copy is only ever the dashboard page, not a file opened directly.
