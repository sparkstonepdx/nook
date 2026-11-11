# Store catalog

One file per app in `apps/`. The `catalog` workflow resolves each app's release on GitHub (the latest,
or `source.tag`), picks the asset for each architecture by `source.assets` pattern, downloads it, pins
its sha256, signs the whole index with the release key and publishes it as the `catalog` release.
Servers fetch that, check the signature and the hash, and never unpack the archive onto disk as root:
nook takes the `binary` out in memory and deploys it with `files` and a generated `nook.json` as a
normal site, as the app's own user.

| Field | |
|---|---|
| `id`, `name`, `description`, `homepage`, `license` | shown in the Store |
| `kind` | `web` (default; gets `site`'s hostname), `service` or `cron` (+ `schedule`) |
| `site` | default site name (`ntfy` → ntfy.&lt;default domain&gt;) |
| `binary` | file name of the executable inside the release asset |
| `files` | other files for the site folder; a web app needs `run` (gets `$PORT`, `$SITE_HOST`, `$HOME/data`) |
| `env` | fields asked for at install: `key`, `desc`, `default` (may use `{site_host}`, `{site}`), `secret`, `required` |
| `needs` | shared services (#12), bound automatically when exactly one provider is ready |
| `health` | path that answers under 400 when it's up; updates that fail it roll back |
| `memory`, `cpu` | limits for the app |
| `source.github`, `source.assets` (GOARCH → regexp), `source.format` (`tar.gz`, `gz`, `raw`), `source.tag` | where releases come from |

Untested entries should be tried on a server (`nook-admin store install ID`) before relying on them;
the health check and rollback guard updates, not first installs.
