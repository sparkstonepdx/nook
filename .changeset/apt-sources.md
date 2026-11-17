---
"nook": minor
---

Store apps can come from apt (#42 step 2), official catalog only: an entry names Debian packages, optionally from a third-party repository the signed catalog defines once and pins by key fingerprint. nook masks the distro's own service and runs the program as an ordinary app. Entries with majors (`versions`) install each as its own app (`postgres-17`), and the install sheet picks the version. Providers' hooks get `PORT`.
