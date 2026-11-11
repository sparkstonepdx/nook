import { For, Show, createResource, createSignal } from "solid-js";
import { A, useParams } from "@solidjs/router";
import { api } from "../api";
import { Field } from "../components";

type Token = { id: string; app: string; label: string; created: number };
type Tokens = { tokens: Token[]; base: string };

const workflow = (url: string) => `name: deploy
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, closed]
concurrency:
  group: deploy-\${{ github.event.pull_request.number || github.ref }}
env:
  NOOK: ${url}
  NOOK_TOKEN: \${{ secrets.NOOK_TOKEN }}
  PR: \${{ github.event.pull_request.number }}
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - if: github.event.action == 'closed'
        run: curl -fsS -X DELETE -H "Authorization: Bearer $NOOK_TOKEN" "$NOOK?pr=$PR"
      - if: github.event.action != 'closed'
        uses: actions/checkout@v4
      - if: github.event.action != 'closed'
        run: npm ci && npm run build
      - if: github.event.action != 'closed'
        run: |
          tar -C dist -czf site.tgz .
          curl --fail-with-body -sS -H "Authorization: Bearer $NOOK_TOKEN" -F site=@site.tgz "$NOOK\${PR:+?pr=$PR}"`;

export default function DeployPage() {
  const params = useParams();
  const [data, { refetch }] = createResource(() => params.app, (app) => api<Tokens>(`/api/apps/${app}/tokens`));
  const [fresh, setFresh] = createSignal<{ token: string; info: Token } | null>(null);
  const [site, setSite] = createSignal(params.app);
  const [err, setErr] = createSignal("");
  const url = () => (data()?.base ?? "") + (site() || "SITE");
  const selfSigned = () => /:\d+\/api\//.test(data()?.base ?? "");

  return (
    <div class="stack">
      <div class="page-head">
        <A class="btn btn-ghost btn-sm" href="/">← Apps</A>
        <h1>{params.app}: deploy</h1>
      </div>
      <p class="text-secondary hint">
        Any CI deploys with one HTTPS request: POST the folder as a .tar.gz to the site's URL with a deploy token.
        Add <span class="mono">?pr=N</span> for a preview, DELETE to remove one, and <span class="mono">-F "env.KEY=value"</span> or <span class="mono">-F env=@.env</span> to send that site's env.
      </p>
      <Show when={data.error}><div class="alert alert-error">{String(data.error.message)}</div></Show>
      <Show when={selfSigned()}>
        <div class="alert">The dashboard has no address yet, so deploys would go to the IP's self-signed certificate, which curl refuses. Set the address on the Domains page.</div>
      </Show>

      <section class="card">
        <h2>Tokens</h2>
        <Show when={fresh()}>
          <div class="alert stack">
            <span>New token <span class="mono">{fresh()!.info.id}</span>. Copy it now; it isn't shown again. Store it as the <span class="mono">NOOK_TOKEN</span> secret.</span>
            <input class="input mono" readonly value={fresh()!.token} onFocus={(e) => e.currentTarget.select()} />
          </div>
        </Show>
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>ID</th><th>Label</th><th>Created</th><th /></tr></thead>
            <tbody>
              <For each={data()?.tokens} fallback={<tr><td colspan={4} class="text-secondary">No tokens. Make one below.</td></tr>}>
                {(t) => (
                  <tr>
                    <td class="mono">{t.id}</td>
                    <td>{t.label}</td>
                    <td>{new Date(t.created * 1000).toLocaleDateString()}</td>
                    <td><div class="actions">
                      <button class="btn btn-ghost btn-sm" onClick={async () => {
                        if (!window.confirm(`Revoke token ${t.id}? Deploys using it stop working right away.`)) return;
                        await api(`/api/apps/${params.app}/tokens/${t.id}/revoke`, {}).catch((e) => setErr(e.message));
                        refetch();
                      }}>Revoke</button>
                    </div></td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
        <form class="form-row" style={{ "margin-top": "0.75rem" }} onSubmit={async (e) => {
          e.preventDefault();
          const form = e.currentTarget;
          setErr("");
          try {
            setFresh(await api(`/api/apps/${params.app}/tokens`, { label: String(new FormData(form).get("label")) }));
            form.reset();
            refetch();
          } catch (x) { setErr((x as Error).message); }
        }}>
          <Field label="Label"><input class="input" name="label" maxLength={40} placeholder="github: owner/repo" /></Field>
          <button class="btn btn-primary">New token</button>
        </form>
        <Show when={err()}><div class="alert alert-error">{err()}</div></Show>
      </section>

      <section class="card stack">
        <h2>GitHub workflow</h2>
        <Field label="Site"><input class="input mono" value={site()} onInput={(e) => setSite(e.currentTarget.value.trim())} placeholder="blog or client.com" /></Field>
        <p class="text-secondary hint">.github/workflows/deploy.yml: pushes to main deploy the site; pull requests deploy a preview and remove it when closed.</p>
        <pre class="output mono" style={{ "overflow-x": "auto", "white-space": "pre" }}><code>{workflow(url())}</code></pre>
      </section>
    </div>
  );
}
