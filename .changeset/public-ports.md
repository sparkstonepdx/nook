---
"nook": minor
---

Public ports (#95): a running site can ask in its nook.json to listen on the server's own ports (`"ports": [25, 993]`), and an admin grants them on the Manage sheet or with `nook-admin ports APP grant 25,993`. Only that unit may bind them, with `CAP_NET_BIND_SERVICE` for ports under 1024; SSH, DNS, Caddy's ports and the app port range are never granted.
