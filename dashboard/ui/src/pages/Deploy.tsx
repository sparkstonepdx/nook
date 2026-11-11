import { For, Show, createResource, createSignal } from "solid-js";
import { A, useParams } from "@solidjs/router";
import { api } from "../api";
import { Field } from "../components";

type Token = { id: string; app: string; label: string; created: number };
type Tokens = { tokens: Token[]; base: string };

export default function DeployPage() {
  const params = useParams();
  const [data, { refetch }] = createResource(() => params.app, (app) => api<Tokens>(`/api/apps/${app}/tokens`));
  const [fresh, setFresh] = createSignal<{ token: string; info: Token } | null>(null);
  const [site, setSite] = createSignal("");
  const [err, setErr] = createSignal("");
  const [howto, { refetch: refetchHowto }] = createResource(() => [params.app, site()] as const, ([app, s]) =>
    api<{ text: string; repo: string; sites: string[] }>(`/api/apps/${app}/instructions?site=${encodeURIComponent(s)}`));
  const [copied, setCopied] = createSignal(false);
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
        <h2>Setup instructions</h2>
        <p class="text-secondary hint">Everything a repo needs to deploy here: the same text <span class="mono">nook-admin create</span> printed (and <span class="mono">nook-admin instructions {params.app}</span> prints any time).</p>
        <div class="form-row">
          <Field label="Site">
            <input class="input mono" list="deploy-sites" value={site()} placeholder={params.app} onChange={(e) => setSite(e.currentTarget.value.trim())} />
            <datalist id="deploy-sites"><For each={howto()?.sites}>{(s) => <option value={s} />}</For></datalist>
          </Field>
          <form class="form-row" onSubmit={async (e) => {
            e.preventDefault();
            setErr("");
            try {
              await api(`/api/apps/${params.app}/repo`, { repo: String(new FormData(e.currentTarget).get("repo")).trim() });
              refetchHowto();
            } catch (x) { setErr((x as Error).message); }
          }}>
            <Field label="GitHub repo"><input class="input mono" name="repo" value={howto()?.repo ?? ""} placeholder="owner/name" /></Field>
            <button class="btn btn-secondary">Save</button>
          </form>
        </div>
        <div><button class="btn btn-ghost btn-sm" onClick={async () => {
          await navigator.clipboard.writeText(howto()?.text ?? "");
          setCopied(true); setTimeout(() => setCopied(false), 1500);
        }}>{copied() ? "Copied" : "Copy all"}</button></div>
        <pre class="output mono" style={{ "overflow-x": "auto", "white-space": "pre" }}><code>{howto()?.text}</code></pre>
      </section>
    </div>
  );
}
