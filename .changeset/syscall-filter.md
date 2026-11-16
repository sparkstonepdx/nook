---
"nook": minor
---

Every running site and cron job gets a syscall filter (#34): systemd's `@system-service` allowlist minus `@privileged` and `@resources`, with blocked calls failing as EPERM. A site that needs one of those calls sets `{"sandbox": {"syscalls": "relaxed"}}` in its nook.json; the dashboard marks it. Units pick it up on each app's next sync. If an app stops starting after this update, check its log for "Operation not permitted" and relax it.
