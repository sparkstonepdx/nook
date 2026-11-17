---
"nook": minor
---

A running site or cron job can opt in to a syscall filter (#34) with `{"sandbox": {"syscalls": "strict"}}` in its nook.json: systemd's `@system-service` allowlist minus `@privileged` and `@resources`, blocked calls failing as EPERM. The dashboard marks it. Opt-in while it's tried app by app.
