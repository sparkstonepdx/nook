import { For, Show, createEffect, createResource, createSignal } from "solid-js";
import { A, useLocation, useNavigate } from "@solidjs/router";
import { api, runCmd } from "../api";
import { Field, StatusBadge } from "../components";

type EnvField = { key: string; desc?: string; default?: string; secret?: boolean; required?: boolean };
type CatalogApp = { id: string; name: string; description: string; homepage?: string; license?: string; version: string; kind?: string; site?: string; needs?: string[]; env?: EnvField[] };
type Row = { app: string; id: string; name: string; version: string; latest: string; available: boolean; site: string; policy: string; url?: string; lastError?: string };
type View = { catalog: CatalogApp[]; fetchedAt: string; error?: string; installed: Row[] };

const policyLabel: Record<string, string> = {
  off: "off: never",
  notify: "notify me",
  patch: "auto: patch",
  minor: "auto: minor + patch",
  all: "auto: every release",
};

export default function Store() {
  const [refresh, setRefresh] = createSignal(false);
  // the source must be truthy or Solid never fetches: wrap the flag in an object
  const [data, { refetch }] = createResource(() => ({ refresh: refresh() }), (s) => api<View>(`/api/store${s.refresh ? "?refresh=1" : ""}`));
  const [picked, setPicked] = createSignal<CatalogApp | null>(null);
  const installedIDs = () => new Set((data()?.installed ?? []).map((r) => r.id));
  const loc = useLocation();
  const browsing = () => loc.pathname.startsWith("/store/install");
  const navigate = useNavigate();

  return (
    <div class="stack">
      <div class="page-head">
        <h1>Store</h1>
        <button class="btn btn-ghost btn-sm" onClick={() => { setRefresh(true); refetch(); }}>Refresh catalog</button>
      </div>
      <p class="text-secondary hint">
        Each app installs as its own nook app: its own user, folder, limits and firewall rules. Downloads are checked against the signed catalog's hashes.
        Updates follow each app's policy, run a health check, and roll back by themselves if the new version doesn't come up.
      </p>
      <Show when={data.error || data()?.error}><div class="alert alert-error">{String(data.error?.message ?? data()?.error)}</div></Show>

      <div class="tab-bar" role="tablist">
        <A href="/store" end class="btn btn-ghost" activeClass="nav-active" role="tab">Installed<Show when={data()?.installed.length}> ({data()!.installed.length})</Show></A>
        <A href="/store/install" class="btn btn-ghost" activeClass="nav-active" role="tab">Install</A>
      </div>

      <Show when={!browsing()}>
      <Show when={data()?.installed.length} fallback={<Show when={data()}><p class="text-secondary">Nothing from the store yet. <A class="link" href="/store/install">Install an app</A>.</p></Show>}>
        <section class="card">
          <div class="table-wrap">
            <table class="table">
              <thead><tr><th>App</th><th>Version</th><th>Updates</th><th /></tr></thead>
              <tbody>
                <For each={data()!.installed}>
                  {(r) => (
                    <tr>
                      <td>
                        <Show when={r.url} fallback={<strong>{r.app}</strong>}><a class="link" href={r.url} target="_blank" rel="noopener"><strong>{r.app}</strong></a></Show>
                        <div class="text-secondary">{r.name}</div>
                      </td>
                      <td>
                        <span class="mono">{r.version}</span>{" "}
                        <Show when={r.available}><StatusBadge warn>{r.latest} available</StatusBadge></Show>
                        <Show when={r.lastError}><div class="text-secondary" title={r.lastError}>last update rolled back</div></Show>
                      </td>
                      <td>
                        <select class="select" onChange={async (e) => { await api(`/api/store/${r.app}/policy`, { policy: e.currentTarget.value }); refetch(); }}>
                          <For each={Object.keys(policyLabel)}>{(p) => <option value={p} selected={p === r.policy}>{policyLabel[p]}</option>}</For>
                        </select>
                      </td>
                      <td><div class="actions">
                        <Show when={r.available}>
                          <button class="btn btn-primary btn-sm" onClick={async () => { await runCmd(`Updated ${r.app}`, `/api/store/${r.app}/update`, {}); refetch(); }}>Update</button>
                        </Show>
                        <a class="btn btn-ghost btn-sm" href={`/apps/${r.app}/env`}>Settings</a>
                      </div></td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
          <p class="text-secondary hint">Remove an app from the Apps page (Delete app). Its data goes with it.</p>
        </section>
      </Show>
      </Show>

      <Show when={browsing()}>
      <div class="store-grid">
        <For each={data()?.catalog} fallback={<Show when={data()}><p class="text-secondary">The catalog is empty.</p></Show>}>
          {(a) => (
            <section class="card stack">
              <div class="page-head"><h2>{a.name}</h2><span class="mono text-secondary">{a.version}</span></div>
              <p>{a.description}</p>
              <div class="text-secondary hint">
                <Show when={a.homepage}><a class="link" href={a.homepage} target="_blank" rel="noopener">{a.homepage}</a> · </Show>
                {a.kind ?? "web"}<Show when={a.license}> · {a.license}</Show>
                <Show when={a.needs?.length}> · needs {a.needs!.join(", ")}</Show>
              </div>
              <div>
                <button class="btn btn-primary btn-sm" onClick={() => setPicked(a)}>{installedIDs().has(a.id) ? "Install another" : "Install"}</button>
              </div>
            </section>
          )}
        </For>
      </div>
      <Show when={data()?.fetchedAt}><p class="text-secondary hint">Catalog checked {new Date(data()!.fetchedAt).toLocaleString()}.</p></Show>
      </Show>

      <InstallDialog app={picked()} onClose={() => setPicked(null)} onDone={() => { refetch(); navigate("/store"); }} taken={installedIDs()} />
    </div>
  );
}

function InstallDialog(props: { app: CatalogApp | null; onClose: () => void; onDone: () => void; taken: Set<string> }) {
  let dialog!: HTMLDialogElement;
  createEffect(() => { if (props.app) dialog.showModal(); else if (dialog.open) dialog.close(); });
  return (
    <dialog ref={dialog} class="dialog" onClose={() => props.onClose()}>
      <Show when={props.app}>
        {(a) => (
          <form class="dialog-box stack" onSubmit={async (e) => {
            e.preventDefault();
            const app = a(); // read everything before the dialog closes and the accessor goes away
            const f = new FormData(e.currentTarget);
            const env: Record<string, string> = {};
            for (const fld of app.env ?? []) env[fld.key] = String(f.get(`env.${fld.key}`) ?? "");
            const body = { id: app.id, app: String(f.get("app")), site: String(f.get("site")), policy: String(f.get("policy")), set: env };
            props.onClose();
            await runCmd(`Installed ${app.name}`, "/api/store/install", body);
            props.onDone();
          }}>
            <h3 class="dialog-header">Install {a().name} {a().version}</h3>
            <div class="form-row">
              <Field label="App name"><input class="input mono" name="app" value={props.taken.has(a().id) ? `${a().id}-2` : a().id} pattern="[a-z][a-z0-9\-]{1,23}" required /></Field>
              <Field label="Site" hint={a().kind === "cron" || a().kind === "service" ? "no hostname" : "subdomain or a granted hostname"}><input class="input mono" name="site" value={a().site ?? a().id} required /></Field>
              <Field label="Updates">
                <select class="select" name="policy"><For each={Object.keys(policyLabel)}>{(p) => <option value={p} selected={p === "minor"}>{policyLabel[p]}</option>}</For></select>
              </Field>
            </div>
            <For each={a().env}>
              {(fld) => (
                <Field label={fld.key + (fld.required ? " *" : "")} hint={fld.desc}>
                  <input class="input mono" name={`env.${fld.key}`} type={fld.secret ? "password" : "text"} placeholder={fld.default} required={fld.required && !fld.default} autocomplete="off" />
                </Field>
              )}
            </For>
            <div class="dialog-actions">
              <button type="button" class="btn btn-ghost" onClick={() => props.onClose()}>Cancel</button>
              <button class="btn btn-primary" type="submit">Install</button>
            </div>
          </form>
        )}
      </Show>
    </dialog>
  );
}
