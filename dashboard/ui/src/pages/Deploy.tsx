import { For, Show, createResource, createSignal } from "solid-js";
import { A, useParams } from "@solidjs/router";
import { api, say } from "../api";
import { Field, PageHead, TokenBox } from "../components";

type Token = { id: string; app: string; label: string; created: number };
type Tokens = { tokens: Token[]; base: string };

export default function DeployPage() {
  const params = useParams();
  const [data, { refetch }] = createResource(() => params.app, (app) => api<Tokens>(`/api/apps/${app}/tokens`));
  const [fresh, setFresh] = createSignal<{ token: string; info: Token } | null>(null);
  const [site, setSite] = createSignal("");
  const [howto, { refetch: refetchHowto }] = createResource(() => [params.app, site()] as const, ([app, s]) =>
    api<{ text: string; repo: string; sites: string[] }>(`/api/apps/${app}/instructions?site=${encodeURIComponent(s)}`));
  const selfSigned = () => /:\d+\/api\//.test(data()?.base ?? "");

  return (
    <>
      <PageHead title="Deploy" back={{ href: "/", label: "Apps" }} detail={params.app} />
      <p class="lede">
        Any CI deploys with one HTTPS request: POST the folder as a .tar.gz to the site's URL with a deploy token.
        Add <span class="mono">?pr=N</span> for a preview, and <span class="mono">-F "env.KEY=value"</span> to send that site's env.
      </p>
      <Show when={data.error}><div class="note note-bad">{String(data.error!.message)}</div></Show>
      <Show when={selfSigned()}>
        <div class="note note-warn">The dashboard has no address yet, so deploys would go to the IP's self-signed certificate, which curl refuses. Set one on the Domains page.</div>
      </Show>

      <section class="section">
        <h2>Tokens</h2>
        <div class="stack">
          <Show when={fresh()}>
            <div class="card stack">
              <p class="cmd-line"><strong>New token</strong> <span class="mono text-secondary">{fresh()!.info.id}</span></p>
              <TokenBox token={fresh()!.token} repo={howto()?.repo} />
            </div>
          </Show>
          <ul class="list">
            <For each={data()?.tokens} fallback={<li class="row"><span class="empty">No tokens. Make one below.</span></li>}>
              {(t) => (
                <li class="row">
                  <span class="row-main"><span>{t.label || "no label"}</span><small><span class="mono">{t.id}</span> · made {new Date(t.created * 1000).toLocaleDateString()}</small></span>
                  <span class="row-end">
                    <button class="btn btn-ghost btn-sm" onClick={async () => {
                      if (!window.confirm(`Revoke token ${t.id}? Deploys using it stop working right away.`)) return;
                      try { await api(`/api/apps/${params.app}/tokens/${t.id}/revoke`, {}); say(`Revoked ${t.id}`, { kind: "success" }); }
                      catch (e) { say("Couldn't revoke it", { kind: "error", detail: (e as Error).message }); }
                      refetch();
                    }}>Revoke</button>
                  </span>
                </li>
              )}
            </For>
          </ul>
          <form class="card" onSubmit={async (e) => {
            e.preventDefault();
            const form = e.currentTarget;
            try {
              setFresh(await api(`/api/apps/${params.app}/tokens`, { label: String(new FormData(form).get("label")) }));
              form.reset();
              refetch();
            } catch (x) { say("Couldn't make a token", { kind: "error", detail: (x as Error).message }); }
          }}>
            <div class="field-row">
              <Field label="Label"><input class="input" name="label" maxLength={40} placeholder="github: owner/repo" /></Field>
              <button class="btn">New token</button>
            </div>
          </form>
        </div>
      </section>

      <section class="section">
        <div class="page-head">
          <h2>Setup instructions</h2>
          <div class="page-actions">
            <button class="btn btn-secondary btn-sm" onClick={async () => { await navigator.clipboard.writeText(howto()?.text ?? ""); say("Instructions copied"); }}>Copy all</button>
          </div>
        </div>
        <div class="stack">
          <p class="hint">The workflow and everything else a repo needs, apart from the token above. <span class="mono">nook-admin instructions {params.app}</span> prints them too.</p>
          <form class="card" onSubmit={async (e) => {
            e.preventDefault();
            try {
              await api(`/api/apps/${params.app}/repo`, { repo: String(new FormData(e.currentTarget).get("repo")).trim() });
              say("Repo saved", { kind: "success" });
              refetchHowto();
            } catch (x) { say("Couldn't save the repo", { kind: "error", detail: (x as Error).message }); }
          }}>
            <div class="field-row">
              <Field label="Site">
                <input class="input mono" list="deploy-sites" value={site()} placeholder={params.app} onChange={(e) => setSite(e.currentTarget.value.trim())} />
                <datalist id="deploy-sites"><For each={howto()?.sites}>{(s) => <option value={s} />}</For></datalist>
              </Field>
              <Field label="GitHub repo"><input class="input mono" name="repo" value={howto()?.repo ?? ""} placeholder="owner/name" /></Field>
              <button class="btn btn-secondary">Save</button>
            </div>
          </form>
          <pre class="output instructions"><code>{howto()?.text}</code></pre>
        </div>
      </section>
    </>
  );
}
