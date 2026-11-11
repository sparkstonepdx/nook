import { For, Show, createResource, createSignal, onCleanup } from "solid-js";
import { A } from "@solidjs/router";
import { api, runCmd } from "../api";
import { StatusBadge, Field, PageHead, Sheet } from "../components";

type Site = { name: string; host: string; running: boolean; live: boolean; port: string; ActiveState?: string; SubState?: string; MainPID?: string; MemoryCurrent?: string; NRestarts?: string; kind: "static" | "web" | "service" | "cron"; schedule?: string; next?: string; last?: string; result?: string };
type App = { name: string; memory: string; memoryMax: string; cpu: string; watcher: string; sites: Site[]; redirects: string[]; grants: string[][] };
type Overview = { baseDomain: string; apps: App[]; system: { free: string; df: string; caddy: string }; defaults: { memory: string; cpu: string; tasks: string } };

const mb = (v?: string) => (v && /^\d+$/.test(v) ? `${Math.round(Number(v) / 1048576)}M` : "");
export const grantLabel = (k: string, n: string) => (k === "domain" ? `${n} + *.${n}` : k === "sub" ? `*.${n}` : n);
const kindTitle = { static: "static files", web: "HTTP server at this hostname", service: "runs on its port with no hostname; only apps bound to it can reach it", cron: "runs on a schedule" };

// A problem worth seeing from the list, or nothing when all is well.
function siteAlert(s: Site) {
  if (s.host && !s.live) return <StatusBadge ok={false}>not live</StatusBadge>;
  if (s.kind === "cron" && (s.ActiveState === "active" || s.ActiveState === "activating")) return <StatusBadge warn>running</StatusBadge>;
  if (s.kind === "cron" && s.last && s.last !== "n/a" && s.result !== "success") return <StatusBadge ok={false}>{s.result}</StatusBadge>;
  if (s.running && s.ActiveState === "failed") return <StatusBadge ok={false}>failed</StatusBadge>;
  if (s.running && s.ActiveState && s.ActiveState !== "active") return <StatusBadge warn>{s.ActiveState}</StatusBadge>;
  return null;
}

// What a site is doing, in one badge.
function siteBadge(s: Site) {
  if (s.host && !s.live) return <StatusBadge ok={false}>not live</StatusBadge>;
  if (s.kind === "cron") {
    if (s.ActiveState === "active" || s.ActiveState === "activating") return <StatusBadge warn>running</StatusBadge>;
    if (s.last && s.last !== "n/a") return <StatusBadge ok={s.result === "success"}>{s.result === "success" ? "ok" : s.result}</StatusBadge>;
    return <StatusBadge>cron</StatusBadge>;
  }
  if (s.running) {
    const st = s.ActiveState || "?";
    return <StatusBadge ok={st === "active" ? true : st === "failed" ? false : undefined} warn={st === "activating" || st === "deactivating"}>{st === "active" ? s.kind : st}</StatusBadge>;
  }
  return <StatusBadge>{s.kind}</StatusBadge>;
}

function siteDetail(s: Site) {
  const parts: string[] = [];
  if (s.kind === "cron") {
    parts.push(s.schedule ?? "");
    if (s.next && s.next !== "n/a") parts.push(`next ${s.next}`);
  } else {
    parts.push(s.kind);
    if (s.running && s.SubState) parts.push(s.SubState);
    if (s.port) parts.push(`port ${s.port}`);
    if (mb(s.MemoryCurrent)) parts.push(mb(s.MemoryCurrent));
    if (Number(s.NRestarts)) parts.push(`${s.NRestarts} restarts`);
  }
  return parts.filter(Boolean).join(" · ");
}

export default function Apps() {
  const [data, { refetch }] = createResource(() => api<Overview>("/api/overview"));
  const timer = setInterval(() => !document.hidden && refetch(), 10_000);
  onCleanup(() => clearInterval(timer));
  const after = async (p?: Promise<unknown>) => { await p; refetch(); };
  const [creating, setCreating] = createSignal(false);
  const [site, setSite] = createSignal<{ app: string; name: string } | null>(null);
  const [manage, setManage] = createSignal<string | null>(null);
  const appOf = (name: string | null | undefined) => data()?.apps.find((a) => a.name === name);
  const siteOf = () => { const s = site(); return s ? appOf(s.app)?.sites.find((x) => x.name === s.name) : undefined; };

  return (
    <>
      <PageHead title="Apps" detail={data()?.baseDomain}>
        <button class="btn" onClick={() => setCreating(true)}>New app</button>
      </PageHead>
      <Show when={data.error}><div class="alert alert-error">{String(data.error!.message)}</div></Show>

      <For each={data()?.apps} fallback={<Show when={data()}><p class="empty">No apps yet.</p></Show>}>
        {(app) => (
          <section class="section">
            <div class="page-head">
              <h2>{app.name}</h2>
              <span class="text-secondary">{mb(app.memory) || app.memory || "?"} of {app.memoryMax || "?"} · cpu {app.cpu || "?"}</span>
              <Show when={app.watcher !== "active"}><StatusBadge ok={false} title="The watcher deploys new folders in sites/">watcher {app.watcher}</StatusBadge></Show>
              <div class="page-actions">
                <button class="btn btn-secondary btn-sm" onClick={() => after(runCmd(`Synced ${app.name}`, `/api/apps/${app.name}/sync`))}>Sync</button>
                <button class="btn btn-secondary btn-sm" onClick={() => setManage(app.name)}>Manage</button>
              </div>
            </div>
            <Show when={app.sites.length} fallback={<p class="empty">No sites yet. <A class="link" href={`/apps/${app.name}/deploy`}>How to deploy</A></p>}>
              <ul class="list">
                <For each={app.sites}>
                  {(s) => (
                    <li><button class="row" onClick={() => setSite({ app: app.name, name: s.name })} title={kindTitle[s.kind]}>
                      <span class="row-main"><span class={s.host ? "" : "mono"}>{s.host || s.name}</span><small>{siteDetail(s)}</small></span>
                      <span class="row-end">{siteAlert(s)}<span class="chev">›</span></span>
                    </button></li>
                  )}
                </For>
              </ul>
            </Show>
          </section>
        )}
      </For>

      <Show when={data()}>
        <section class="section">
          <div class="page-head">
            <h2>Server</h2>
            <StatusBadge ok={data()!.system.caddy === "active"}>caddy {data()!.system.caddy}</StatusBadge>
            <div class="page-actions">
              <button class="btn btn-secondary btn-sm" onClick={() => after(runCmd("Synced all apps", "/api/sync-all"))}>Sync all</button>
              <button class="btn btn-secondary btn-sm" onClick={() => after(runCmd("Reloaded Caddy", "/api/caddy/reload"))}>Reload Caddy</button>
            </div>
          </div>
          <pre class="output">{data()!.system.free}{"\n"}{data()!.system.df}</pre>
        </section>
      </Show>

      <Show when={data()}>{(d) => <NewApp open={creating()} defaults={d().defaults} onClose={() => setCreating(false)} onDone={() => refetch()} />}</Show>
      <SiteSheet app={site()?.app ?? ""} site={siteOf()} onClose={() => setSite(null)} after={after} />
      <ManageSheet app={appOf(manage())} onClose={() => setManage(null)} after={after} />
    </>
  );
}

function NewApp(props: { open: boolean; defaults: Overview["defaults"]; onClose: () => void; onDone: () => void }) {
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    const form = e.currentTarget as HTMLFormElement;
    const f = Object.fromEntries(new FormData(form));
    props.onClose();
    const r = await runCmd(`Created ${f.name}`, "/api/apps", f, "The deploy token below is shown once: add it to the repo as NOOK_TOKEN now. The rest of these instructions are on the app's Deploy page any time.");
    if (r.ok) form.reset();
    props.onDone();
  };
  return (
    <Sheet open={props.open} title="New app" onClose={props.onClose}
      footer={<><button class="btn btn-secondary" onClick={props.onClose}>Cancel</button><button class="btn" type="submit" form="new-app">Create</button></>}>
      <form id="new-app" onSubmit={submit}>
        <Field label="Name"><input class="input" name="name" required pattern="[a-z][a-z0-9\-]{1,23}" placeholder="myapp" autocomplete="off" /></Field>
        <div class="field-row">
          <Field label="Memory" hint="for all its processes"><input class="input" name="memory" value={props.defaults.memory} required /></Field>
          <Field label="CPU" hint="100% is one core"><input class="input" name="cpu" value={props.defaults.cpu} required /></Field>
          <Field label="Tasks" hint="processes and threads"><input class="input" name="tasks" value={props.defaults.tasks} required /></Field>
        </div>
        <Field label="GitHub repo" hint="Optional. Used in the deploy instructions."><input class="input" name="repo" placeholder="sparkstonepdx/myapp" pattern="[A-Za-z0-9_.\-]+/[A-Za-z0-9_.\-]+" /></Field>
      </form>
    </Sheet>
  );
}

function SiteSheet(props: { app: string; site?: Site; onClose: () => void; after: (p?: Promise<unknown>) => void }) {
  const op = (o: string, title: string) => { const s = props.site!; props.onClose(); props.after(runCmd(title, `/api/apps/${props.app}/sites/${encodeURIComponent(s.name)}/${o}`)); };
  const name = () => props.site?.host || props.site?.name || "";
  return (
    <Sheet open={!!props.site} title={name()} onClose={props.onClose}>
      <Show when={props.site}>
        {(s) => (
          <div class="stack">
            <p class="cmd-line">{siteBadge(s())} <span class="text-secondary">{kindTitle[s().kind]}</span></p>
            <Show when={s().host && !s().live}>
              <div class="alert alert-error">Sync skipped this hostname: it isn't allowed for {props.app}, it's reserved, or another app has it. Sync shows which.</div>
            </Show>
            <dl class="facts">
              <Show when={s().kind === "cron"}><dt>Schedule</dt><dd class="mono">{s().schedule}</dd><dt>Next</dt><dd>{s().next && s().next !== "n/a" ? s().next : "not scheduled"}</dd><dt>Last run</dt><dd>{s().last && s().last !== "n/a" ? `${s().last} (${s().result})` : "never"}</dd></Show>
              <Show when={s().running}><dt>State</dt><dd>{s().ActiveState} · {s().SubState}</dd><Show when={s().MainPID && s().MainPID !== "0"}><dt>PID</dt><dd class="mono">{s().MainPID}</dd></Show><dt>Restarts</dt><dd>{s().NRestarts || 0}</dd></Show>
              <Show when={s().port}><dt>Port</dt><dd class="mono">{s().port}</dd></Show>
              <Show when={mb(s().MemoryCurrent)}><dt>Memory</dt><dd>{mb(s().MemoryCurrent)}</dd></Show>
              <dt>Folder</dt><dd class="mono">sites/{s().name}</dd>
            </dl>
            <div class="actions-grid">
              <Show when={s().host && s().live}><a class="btn" href={`https://${s().host}`} target="_blank" rel="noopener">Open site</a></Show>
              <Show when={s().kind === "cron"}><button class="btn btn-secondary" onClick={() => op("restart", `Started ${s().name}`)}>Run now</button></Show>
              <Show when={s().running}>
                <button class="btn btn-secondary" onClick={() => op("restart", `Restarted ${name()}`)}>Restart</button>
                <Show when={s().ActiveState === "active"} fallback={<button class="btn btn-secondary" onClick={() => op("start", `Started ${name()}`)}>Start</button>}>
                  <button class="btn btn-secondary" onClick={() => op("stop", `Stopped ${name()}`)}>Stop</button>
                </Show>
              </Show>
              <Show when={s().running || s().kind === "cron"}><A class="btn btn-secondary" href={`/apps/${props.app}/sites/${encodeURIComponent(s().name)}/logs`}>Logs</A></Show>
            </div>
            <button class="btn btn-ghost danger remove" onClick={() => window.confirm(`Remove ${name()}? This deletes sites/${s().name}.`) && op("remove", `Removed ${name()}`)}>Remove site</button>
          </div>
        )}
      </Show>
    </Sheet>
  );
}

function ManageSheet(props: { app?: App; onClose: () => void; after: (p?: Promise<unknown>) => void }) {
  const [confirm, setConfirm] = createSignal("");
  const a = () => props.app!;
  return (
    <Sheet open={!!props.app} title={props.app?.name ?? ""} onClose={() => { setConfirm(""); props.onClose(); }}>
      <Show when={props.app}>
        <div class="stack">
          <ul class="list">
            <li><A class="row" href={`/apps/${a().name}/env`}><span class="row-main"><span>Environment</span><small>Secrets and settings its sites load</small></span><span class="row-end"><span class="chev">›</span></span></A></li>
            <li><A class="row" href={`/apps/${a().name}/deploy`}><span class="row-main"><span>Deploy</span><small>Tokens and the setup for its repo</small></span><span class="row-end"><span class="chev">›</span></span></A></li>
            <li><A class="row" href={`/apps/${a().name}/sync-log`}><span class="row-main"><span>Sync log</span><small>What the last syncs did</small></span><span class="row-end"><span class="chev">›</span></span></A></li>
            <li><A class="row" href="/domains"><span class="row-main"><span>Domains</span><small>{a().grants.length ? a().grants.map(([, k, n]) => grantLabel(k, n)).join(", ") : "Nothing allowed yet"}</small></span><span class="row-end"><span class="chev">›</span></span></A></li>
          </ul>
          <Show when={a().redirects.length}><p class="hint">{a().redirects.join(", ")} redirect to the root domain.</p></Show>
          <form class="card-border danger-zone" onSubmit={(e) => { e.preventDefault(); const n = a().name; setConfirm(""); props.onClose(); props.after(runCmd(`Deleted ${n}`, `/api/apps/${n}/delete`, { confirm: n })); }}>
            <div class="field-row">
              <Field label={`Delete ${a().name}`} hint="Its folder, user and everything in it. Type its name to confirm.">
                <input class="input" value={confirm()} onInput={(e) => setConfirm(e.currentTarget.value)} autocomplete="off" placeholder={a().name} />
              </Field>
            </div>
            <button class="btn danger-fill delete" disabled={confirm() !== a().name}>Delete {a().name}</button>
          </form>
        </div>
      </Show>
    </Sheet>
  );
}
