import { For, Show, createResource, createSignal, onCleanup } from "solid-js";
import { A } from "@solidjs/router";
import { api, runCmd } from "../api";
import { StatusBadge, Field } from "../components";

type Site = { name: string; host: string; running: boolean; live: boolean; port: string; ActiveState?: string; SubState?: string; MainPID?: string; MemoryCurrent?: string; NRestarts?: string };
type App = { name: string; memory: string; memoryMax: string; cpu: string; watcher: string; sites: Site[]; redirects: string[]; grants: string[][] };
type Overview = { baseDomain: string; apps: App[]; system: { free: string; df: string; caddy: string }; defaults: { memory: string; cpu: string; tasks: string } };

const mb = (v?: string) => (v && /^\d+$/.test(v) ? `${Math.round(Number(v) / 1048576)}M` : "");
export const grantLabel = (k: string, n: string) => (k === "domain" ? `${n} + *.${n}` : k === "sub" ? `*.${n}` : n);

export default function Apps() {
  const [data, { refetch }] = createResource(() => api<Overview>("/api/overview"));
  const timer = setInterval(() => !document.hidden && refetch(), 10_000);
  onCleanup(() => clearInterval(timer));
  const after = async (p?: Promise<unknown>) => { await p; refetch(); };

  return (
    <div class="stack">
      <div class="page-head">
        <h1>Apps</h1>
        <Show when={data()}><span class="text-secondary">{data()!.baseDomain}</span>
          <StatusBadge ok={data()!.system.caddy === "active"}>caddy {data()!.system.caddy}</StatusBadge></Show>
        <span class="spacer" />
        <button class="btn btn-secondary" onClick={() => after(runCmd("Sync all apps", "/api/sync-all"))}>Sync all</button>
        <button class="btn btn-secondary" onClick={() => after(runCmd("Reload Caddy", "/api/caddy/reload"))}>Reload Caddy</button>
      </div>

      <Show when={data()}>{(d) => <NewApp defaults={d().defaults} onDone={() => refetch()} />}</Show>
      <Show when={data.error}><div class="alert alert-error">{String(data.error.message)}</div></Show>

      <For each={data()?.apps} fallback={<Show when={data()}><p class="text-secondary">No apps yet.</p></Show>}>
        {(app) => <AppCard app={app} after={after} />}
      </For>

      <Show when={data()}>
        <section class="card-border">
          <h2>System</h2>
          <pre class="output mono">{data()!.system.free}{"\n"}{data()!.system.df}</pre>
        </section>
      </Show>
    </div>
  );
}

function NewApp(props: { defaults: Overview["defaults"]; onDone: () => void }) {
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    const form = e.currentTarget as HTMLFormElement;
    const f = Object.fromEntries(new FormData(form));
    const r = await runCmd(`Created ${f.name}`, "/api/apps", f, "The NOOK_TOKEN secret below holds the deploy key and is shown once. Add it to the repo now.");
    if (r?.ok) form.reset();
    props.onDone();
  };
  return (
    <details class="card-border">
      <summary><strong>New app</strong></summary>
      <form class="form-row" onSubmit={submit} style={{ "margin-top": "1rem" }}>
        <Field label="Name"><input class="input" name="name" required pattern="[a-z][a-z0-9\-]{1,23}" placeholder="myapp" /></Field>
        <Field label="Memory" hint="RAM cap for all its processes"><input class="input" name="memory" value={props.defaults.memory} required /></Field>
        <Field label="CPU" hint="100% = one core"><input class="input" name="cpu" value={props.defaults.cpu} required /></Field>
        <Field label="Tasks" hint="max processes + threads"><input class="input" name="tasks" value={props.defaults.tasks} required /></Field>
        <Field label="GitHub repo" hint="optional, for the gh command"><input class="input" name="repo" placeholder="sparkstonepdx/myapp" pattern="[A-Za-z0-9_.\-]+/[A-Za-z0-9_.\-]+" /></Field>
        <button class="btn btn-primary">Create</button>
      </form>
    </details>
  );
}

function AppCard(props: { app: App; after: (p?: Promise<unknown>) => void }) {
  const a = () => props.app;
  const [confirm, setConfirm] = createSignal("");
  const siteOp = (s: Site, op: string, title: string) => props.after(runCmd(title, `/api/apps/${a().name}/sites/${encodeURIComponent(s.name)}/${op}`));
  return (
    <section class="card app-card">
      <header>
        <h2>{a().name}</h2>
        <div class="meta text-secondary">
          <span>memory {mb(a().memory) || a().memory || "?"} / {a().memoryMax || "?"}</span>
          <span>cpu {a().cpu || "?"}</span>
          <span>watcher <StatusBadge ok={a().watcher === "active"}>{a().watcher}</StatusBadge></span>
        </div>
        <span class="spacer" />
        <A class="btn btn-ghost btn-sm" href={`/apps/${a().name}/env`}>Environment</A>
        <A class="btn btn-ghost btn-sm" href={`/apps/${a().name}/deploy`}>Deploy</A>
        <A class="btn btn-ghost btn-sm" href={`/apps/${a().name}/sync-log`}>Sync log</A>
        <button class="btn btn-secondary btn-sm" onClick={() => props.after(runCmd(`Synced ${a().name}`, `/api/apps/${a().name}/sync`))}>Sync</button>
      </header>

      <Show when={a().sites.length} fallback={<p class="text-secondary">No sites yet.</p>}>
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>Site</th><th>Type</th><th>State</th><th>Port</th><th>Memory</th><th /></tr></thead>
            <tbody>
              <For each={a().sites}>
                {(s) => (
                  <tr>
                    <td>
                      <a class="link" href={`https://${s.host}`} target="_blank" rel="noopener">{s.host}</a>{" "}
                      <Show when={!s.live}><StatusBadge ok={false} title="Skipped by sync: not granted, reserved, or claimed by another app. Run Sync to see why.">not live</StatusBadge></Show>
                    </td>
                    <td><span class="badge">{s.running ? "app" : "static"}</span></td>
                    <td>
                      <Show when={s.running}>
                        <StatusBadge ok={s.ActiveState === "active"}>{s.ActiveState || "?"}</StatusBadge>{" "}
                        <span class="text-secondary">{s.SubState}{s.MainPID && s.MainPID !== "0" ? ` · pid ${s.MainPID}` : ""}{Number(s.NRestarts) ? ` · ${s.NRestarts} restarts` : ""}</span>
                      </Show>
                    </td>
                    <td class="mono">{s.port}</td>
                    <td>{mb(s.MemoryCurrent)}</td>
                    <td>
                      <div class="actions">
                        <Show when={s.running}>
                          <button class="btn btn-secondary btn-sm" onClick={() => siteOp(s, "restart", `Restarted ${s.host}`)}>Restart</button>
                          <button class="btn btn-secondary btn-sm" onClick={() => siteOp(s, "stop", `Stopped ${s.host}`)}>Stop</button>
                          <button class="btn btn-secondary btn-sm" onClick={() => siteOp(s, "start", `Started ${s.host}`)}>Start</button>
                          <A class="btn btn-ghost btn-sm" href={`/apps/${a().name}/sites/${encodeURIComponent(s.name)}/logs`}>Logs</A>
                        </Show>
                        <button class="btn btn-ghost btn-sm" onClick={() => confirm_(`Remove ${s.host}? This deletes sites/${s.name}.`) && siteOp(s, "remove", `Removed ${s.host}`)}>Remove</button>
                      </div>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>

      <div class="text-secondary hint">
        <Show when={a().redirects.length}><div>Redirects: {a().redirects.join(", ")} → root domain</div></Show>
        <div>Allowed: {a().grants.length ? a().grants.map(([, k, n]) => grantLabel(k, n)).join(", ") : "nothing"} · <A class="link" href="/domains">edit</A></div>
      </div>

      <details class="danger">
        <summary class="text-secondary">Delete app</summary>
        <form class="form-row" style={{ "margin-top": "0.75rem" }} onSubmit={(e) => { e.preventDefault(); props.after(runCmd(`Deleted ${a().name}`, `/api/apps/${a().name}/delete`, { confirm: confirm() })); }}>
          <Field label={`Type ${a().name} to delete its folder, user and everything in it`}>
            <input class="input" value={confirm()} onInput={(e) => setConfirm(e.currentTarget.value)} autocomplete="off" />
          </Field>
          <button class="btn btn-primary text-error" disabled={confirm() !== a().name}>Delete {a().name}</button>
        </form>
      </details>
    </section>
  );
}

const confirm_ = (msg: string) => window.confirm(msg);
