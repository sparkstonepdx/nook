---
"nook": minor
---

Apt repositories in the catalog can be flat (`"suite": "./"`, no components), as Collabora's is, and can pin more than one key (`"keys"`) for a key file that holds a rotated key next to its successor. Every key in the file must be pinned, since apt trusts all of them for the repository.
