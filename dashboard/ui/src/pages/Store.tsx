import { For, Show, createResource, createSignal } from "solid-js";
import { A, useLocation, useNavigate } from "@solidjs/router";
import { api, runCmd, say } from "../api";
import { Field, PageHead, Sheet, StatusBadge } from "../components";

type EnvField = { key: string; desc?: string; default?: string; secret?: boolean; required?: boolean };
type CatalogApp = { id: string; name: string; description: string; homepage?: string; license?: string; version: string; kind?: string; site?: string; needs?: string[]; env?: EnvField[] };
type Row = { app: string; id: string; name: string; version: string; latest: string; available: boolean; site: string; policy: string; url?: string; lastError?: string };
type View = { catalog: CatalogApp[]; fetchedAt: string; error?: string; installed: Row[] };

const policyLabel: Record<string, string> = {
  off: "Never",
  notify: "Tell me",
  patch: "Fixes only",
  minor: "Fixes and features",
  all: "Every release",
};
const policyHint: Record<string, string> = {
  off: "No checks for this app.",
  notify: "A notification when there's a new version; you update it here.",
  patch: "Installs bug-fix releases by itself (1.2.x).",
  minor: "Installs anything that isn't a major version by itself.",
  all: "Installs every release by itself, major versions too.",
};

export default function Store() {
  const [refresh, setRefresh] = createSignal(false);
  // the source must be truthy or Solid never fetches: wrap the flag in an object
  const [data, { refetch }] = createResource(() => ({ refresh: refresh() }), (s) => api<View>(`/api/store${s.refresh ? "?refresh=1" : ""}`));
  const [picked, setPicked] = createSignal<CatalogApp | null>(null);
  const [open, setOpen] = createSignal<string | null>(null);
  const installedIDs = () => new Set((data()?.installed ?? []).map((r) => r.id));
  const loc = useLocation();
  const browsing = () => loc.pathname.startsWith("/store/install");
  const navigate = useNavigate();
  const row = () => data()?.installed.find((r) => r.app === open());

  return (
    <>
      <PageHead title="Store">
        <button class="btn btn-ghost btn-sm" onClick={() => { setRefresh(true); refetch(); }}>Refresh catalog</button>
      </PageHead>
      <p class="lede">Each app installs as its own nook app, with its own user, folder and limits. Updates roll back by themselves if the new version doesn't come up.</p>
      <Show when={data.error || data()?.error}><div class="alert alert-error">{String(data.error?.message ?? data()?.error)}</div></Show>

      <nav class="tabs" role="tablist">
        <A href="/store" end class="tab" activeClass="tab-active" role="tab">Installed<Show when={data()?.installed.length}> ({data()!.installed.length})</Show></A>
        <A href="/store/install" class="tab" activeClass="tab-active" role="tab">Install</A>
      </nav>

      <Show when={!browsing()}>
        <Show when={data()?.installed.length} fallback={<Show when={data()}><p class="empty">Nothing from the store yet. <A class="link" href="/store/install">Install an app</A></p></Show>}>
          <ul class="list">
            <For each={data()!.installed}>
              {(r) => (
                <li><button class="row" onClick={() => setOpen(r.app)}>
                  <span class="row-main"><span>{r.app}</span><small>{r.name} <span class="mono">{r.version}</span> · updates: {policyLabel[r.policy]?.toLowerCase()}</small></span>
                  <span class="row-end">
                    <Show when={r.lastError}><StatusBadge ok={false} title={r.lastError}>rolled back</StatusBadge></Show>
                    <Show when={r.available}><StatusBadge warn>{r.latest}</StatusBadge></Show>
                    <span class="chev">›</span>
                  </span>
                </button></li>
              )}
            </For>
          </ul>
          <p class="hint">Remove an app from its Manage sheet on the Apps page. Its data goes with it.</p>
        </Show>
      </Show>

      <Show when={browsing()}>
        <ul class="list">
          <For each={data()?.catalog} fallback={<Show when={data()}><li class="row"><span class="empty">The catalog is empty.</span></li></Show>}>
            {(a) => (
              <li class="row">
                <span class="row-main">
                  <span>{a.name} <span class="mono text-secondary">{a.version}</span></span>
                  <small>{a.description}</small>
                  <small>{a.kind ?? "web"}<Show when={a.license}> · {a.license}</Show><Show when={a.needs?.length}> · needs {a.needs!.join(", ")}</Show><Show when={a.homepage}> · <a class="link" href={a.homepage} target="_blank" rel="noopener">homepage</a></Show></small>
                </span>
                <span class="row-end"><button class="btn btn-sm" onClick={() => setPicked(a)}>{installedIDs().has(a.id) ? "Add another" : "Install"}</button></span>
              </li>
            )}
          </For>
        </ul>
        <Show when={data()?.fetchedAt}><p class="hint">Catalog checked {new Date(data()!.fetchedAt).toLocaleString()}.</p></Show>
      </Show>

      <InstallSheet app={picked()} onClose={() => setPicked(null)} onDone={() => { refetch(); navigate("/store"); }} taken={installedIDs()} />
      <InstalledSheet row={row()} onClose={() => setOpen(null)} onDone={() => refetch()} />
    </>
  );
}

function InstalledSheet(props: { row?: Row; onClose: () => void; onDone: () => void }) {
  return (
    <Sheet open={!!props.row} title={props.row?.app ?? ""} onClose={props.onClose}>
      <Show when={props.row}>
        {(r) => (
          <div class="stack">
            <dl class="facts">
              <dt>App</dt><dd>{r().name}</dd>
              <dt>Version</dt><dd class="mono">{r().version}<Show when={r().available}> · {r().latest} available</Show></dd>
              <dt>Site</dt><dd><Show when={r().url} fallback={<span class="mono">{r().site}</span>}><a class="link" href={r().url} target="_blank" rel="noopener">{r().site}</a></Show></dd>
            </dl>
            <Show when={r().lastError}><div class="alert alert-error">The last update didn't come up, so it was rolled back: {r().lastError}</div></Show>
            <Field label="Updates" hint={policyHint[r().policy]}>
              <select class="select" onChange={async (e) => {
                const p = e.currentTarget.value;
                try { await api(`/api/store/${r().app}/policy`, { policy: p }); say(`${r().app}: ${policyLabel[p].toLowerCase()}`, { kind: "success" }); }
                catch (x) { say("Couldn't change updates", { kind: "error", detail: (x as Error).message }); }
                props.onDone();
              }}>
                <For each={Object.keys(policyLabel)}>{(p) => <option value={p} selected={p === r().policy}>{policyLabel[p]}</option>}</For>
              </select>
            </Field>
            <div class="actions-grid">
              <Show when={r().available}>
                <button class="btn" onClick={async () => { const a = r().app, v = r().latest; props.onClose(); await runCmd(`Updated ${a} to ${v}`, `/api/store/${a}/update`, {}); props.onDone(); }}>Update to {r().latest}</button>
              </Show>
              <A class="btn btn-secondary" href={`/apps/${r().app}/env`}>Settings</A>
            </div>
          </div>
        )}
      </Show>
    </Sheet>
  );
}

function InstallSheet(props: { app: CatalogApp | null; onClose: () => void; onDone: () => void; taken: Set<string> }) {
  return (
    <Sheet open={!!props.app} title={props.app ? `Install ${props.app.name}` : ""} onClose={props.onClose}
      footer={<><button type="button" class="btn btn-secondary" onClick={props.onClose}>Cancel</button><button class="btn" type="submit" form="store-install">Install</button></>}>
      <Show when={props.app}>
        {(a) => (
          <form id="store-install" onSubmit={async (e) => {
            e.preventDefault();
            const app = a(); // read everything before the sheet closes and the accessor goes away
            const f = new FormData(e.currentTarget);
            const env: Record<string, string> = {};
            for (const fld of app.env ?? []) env[fld.key] = String(f.get(`env.${fld.key}`) ?? "");
            const body = { id: app.id, app: String(f.get("app")), site: String(f.get("site")), policy: String(f.get("policy")), set: env };
            props.onClose();
            const r = await runCmd(`Installed ${app.name}`, "/api/store/install", body);
            if (r.ok) props.onDone();
          }}>
            <p class="hint">{a().description} Version <span class="mono">{a().version}</span>.</p>
            <div class="field-row">
              <Field label="App name"><input class="input mono" name="app" value={props.taken.has(a().id) ? `${a().id}-2` : a().id} pattern="[a-z][a-z0-9\-]{1,23}" required /></Field>
              <Field label="Site"><input class="input mono" name="site" value={a().site ?? a().id} required /></Field>
            </div>
            <p class="hint">{a().kind === "cron" || a().kind === "service" ? "It runs without a hostname." : "A subdomain, or a hostname the app is allowed."}</p>
            <Field label="Updates">
              <select class="select" name="policy"><For each={Object.keys(policyLabel)}>{(p) => <option value={p} selected={p === "minor"}>{policyLabel[p]}</option>}</For></select>
            </Field>
            <For each={a().env}>
              {(fld) => (
                <Field label={fld.key + (fld.required ? " *" : "")} hint={fld.desc}>
                  <input class="input mono" name={`env.${fld.key}`} type={fld.secret ? "password" : "text"} placeholder={fld.default} required={fld.required && !fld.default} autocomplete="off" />
                </Field>
              )}
            </For>
          </form>
        )}
      </Show>
    </Sheet>
  );
}
