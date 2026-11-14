import { For, Show, createResource, createSignal, onCleanup } from "solid-js";
import { A } from "@solidjs/router";
import { api, runCmd, setResult, type CmdResult } from "../api";
import { StatusBadge, Field, PageHead, Sheet, TokenBox, Confirm } from "../components";
import Server from "./Server";

type Site = { name: string; host: string; running: boolean; live: boolean; port: string; ActiveState?: string; SubState?: string; MainPID?: string; MemoryCurrent?: string; NRestarts?: string; kind: "static" | "web" | "service" | "cron"; schedule?: string; next?: string; last?: string; result?: string; health?: string; healthy?: boolean; healthError?: string };
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
  if (s.healthy === false) return <StatusBadge ok={false} title={s.healthError}>unhealthy</StatusBadge>;
  if (s.running && s.ActiveState && s.ActiveState !== "active") return <StatusBadge warn>{s.ActiveState}</StatusBadge>;
  return null;
}

// A problem worth opening the app for (not "running" or "activating", which pass by themselves).
function siteIssue(s: Site) {
  return (s.host && !s.live) || (s.kind === "cron" && s.last && s.last !== "n/a" && s.result !== "success" && s.ActiveState !== "active" && s.ActiveState !== "activating")
    || (s.running && s.ActiveState === "failed") || s.healthy === false;
}

// Sites with a problem first, then by name, so an open app answers "what's wrong" before scrolling.
function sortedSites(sites: Site[]) {
  return [...sites].sort((a, b) => Number(!!siteIssue(b)) - Number(!!siteIssue(a)) || (a.host || a.name).localeCompare(b.host || b.name));
}

// Which apps are open, per browser: a convenience, so a failed read just means all closed.
const OPEN_KEY = "nook-open-apps";
function readOpen(): Set<string> {
  try { return new Set(JSON.parse(localStorage.getItem(OPEN_KEY) || "[]")); } catch { return new Set(); }
}
function writeOpen(v: Set<string>) {
  try { localStorage.setItem(OPEN_KEY, JSON.stringify([...v])); } catch { /* storage off */ }
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
  const [limits, setLimits] = createSignal<string | null>(null);
  const appOf = (name: string | null | undefined) => data()?.apps.find((a) => a.name === name);
  const [open, setOpen] = createSignal(readOpen());
  const isOpen = (name: string) => open().has(name) || data()?.apps.length === 1;
  const toggle = (name: string) => { const v = new Set(open()); v.has(name) ? v.delete(name) : v.add(name); setOpen(v); writeOpen(v); };
  const allOpen = () => !!data()?.apps.length && data()!.apps.every((a) => open().has(a.name));
  const setAll = (on: boolean) => { const v = new Set(on ? data()?.apps.map((a) => a.name) : []); setOpen(v); writeOpen(v); };
  const siteOf = () => { const s = site(); return s ? appOf(s.app)?.sites.find((x) => x.name === s.name) : undefined; };

  return (
    <>
      <PageHead title="Apps" detail={data()?.baseDomain}>
        <button class="btn" onClick={() => setCreating(true)}>New app</button>
      </PageHead>
      <Show when={data.error}><div class="note note-bad">{String(data.error!.message)}</div></Show>

      <Show when={(data()?.apps.length ?? 0) > 1}>
        <div class="apps-tools"><button class="btn btn-ghost btn-sm" onClick={() => setAll(!allOpen())}>{allOpen() ? "Collapse all" : "Expand all"}</button></div>
      </Show>
      <Show when={data()?.apps.length} fallback={<Show when={data()}><p class="empty">No apps yet.</p></Show>}>
        <ul class="list apps">
          <For each={data()!.apps}>
            {(app) => {
              const issues = () => app.sites.filter(siteIssue).length + (app.watcher !== "active" ? 1 : 0);
              return (
                <li classList={{ "app-open": isOpen(app.name) }}>
                  <button class="row app-row" aria-expanded={isOpen(app.name)} aria-controls={`app-${app.name}`} onClick={() => toggle(app.name)}>
                    <span class="app-chev" aria-hidden="true">›</span>
                    <span class="row-main">
                      <span class="app-name">{app.name}</span>
                      <small>{app.sites.length} {app.sites.length === 1 ? "site" : "sites"} · {mb(app.memory) || app.memory || "?"} of {app.memoryMax || "?"} · cpu {cpuPct(app.cpu)}</small>
                    </span>
                    <span class="row-end"><Show when={issues()}><StatusBadge ok={false}>{issues()} {issues() === 1 ? "problem" : "problems"}</StatusBadge></Show></span>
                  </button>
                  <Show when={isOpen(app.name)}>
                    <div class="app-body" id={`app-${app.name}`}>
                      <div class="app-tools">
                        <Show when={app.watcher !== "active"} fallback={<span />}>
                          <span class="app-warn">Watcher {app.watcher}: new folders won't deploy</span>
                        </Show>
                        <span class="app-actions">
                          <button class="btn btn-ghost btn-sm" onClick={() => after(runCmd(`Synced ${app.name}`, `/api/apps/${app.name}/sync`))}>Sync</button>
                          <button class="btn btn-ghost btn-sm" onClick={() => setManage(app.name)}>Manage</button>
                        </span>
                      </div>
                      <Show when={app.sites.length} fallback={<p class="empty">No sites yet. <A class="link" href={`/apps/${app.name}/deploy`}>How to deploy</A></p>}>
                        <ul class="site-list">
                          <For each={sortedSites(app.sites)}>
                            {(s) => (
                              <li><button class="site-row" onClick={() => setSite({ app: app.name, name: s.name })} title={kindTitle[s.kind]}>
                                <span class="site-main">
                                  <span class={`site-name${s.host ? "" : " mono"}`}>{s.host || s.name}</span>
                                  <Show when={siteDetail(s) !== "static"}><small>{siteDetail(s)}</small></Show>
                                </span>
                                <span class="row-end">{siteAlert(s)}<span class="chev">›</span></span>
                              </button></li>
                            )}
                          </For>
                        </ul>
                      </Show>
                    </div>
                  </Show>
                </li>
              );
            }}
          </For>
        </ul>
      </Show>

      <Show when={data()}>
        <Server caddy={data()!.system.caddy} onLimits={(a) => setLimits(a)} after={after} />
      </Show>

      <Show when={data()}>{(d) => <NewApp open={creating()} defaults={d().defaults} onClose={() => setCreating(false)} onDone={() => refetch()} />}</Show>
      <SiteSheet app={site()?.app ?? ""} site={siteOf()} onClose={() => setSite(null)} after={after} />
      <ManageSheet app={appOf(manage())} onClose={() => setManage(null)} after={after} onLimits={(a) => { setManage(null); setLimits(a); }} />
      <LimitsSheet app={appOf(limits())} onClose={() => setLimits(null)} after={after} />
    </>
  );
}

function NewApp(props: { open: boolean; defaults: Overview["defaults"]; onClose: () => void; onDone: () => void }) {
  const [created, setCreated] = createSignal<{ app: string; token: string; repo: string } | null>(null);
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    const form = e.currentTarget as HTMLFormElement;
    const f = Object.fromEntries(new FormData(form)) as Record<string, string>;
    props.onClose();
    let r: CmdResult & { data?: { token: string; repo: string } };
    try { r = await api("/api/apps", f); } catch (x) { r = { ok: false, code: -1, cmd: "", out: (x as Error).message }; }
    if (r.ok && r.data?.token) { form.reset(); setCreated({ app: f.name, token: r.data.token, repo: r.data.repo }); }
    else setResult({ ...r, title: r.ok ? `Created ${f.name}` : `Couldn't create ${f.name}` });
    props.onDone();
  };
  return (<>
    <Sheet open={!!created()} title={`Created ${created()?.app ?? ""}`} onClose={() => setCreated(null)}
      footer={<><button class="btn btn-secondary" onClick={() => setCreated(null)}>Done</button><A class="btn" href={`/apps/${created()?.app}/deploy`}>Setup instructions</A></>}>
      <Show when={created()}>{(c) => (
        <div class="stack">
          <TokenBox token={c().token} repo={c().repo} />
          <p class="hint">The workflow to copy into the repo, what to publish, env and services are on the app's Deploy page, any time.</p>
        </div>
      )}</Show>
    </Sheet>
    <Sheet open={props.open} title="New app" onClose={props.onClose}
      footer={<><button class="btn btn-secondary" onClick={props.onClose}>Cancel</button><button class="btn" type="submit" form="new-app">Create</button></>}>
      <form id="new-app" onSubmit={submit}>
        <Field label="Name"><input class="input" name="name" required pattern="[a-z][a-z0-9\-]{1,23}" placeholder="myapp" autocomplete="off" /></Field>
        <div class="field-row">
          <Field label="Memory" hint="for all its processes"><input class="input" name="memory" value={props.defaults.memory} required /></Field>
          <Field label="CPU" hint="100% is one core"><input class="input" name="cpu" value={props.defaults.cpu} required /></Field>
          <Field label="Tasks" hint="processes and threads"><input class="input" name="tasks" value={props.defaults.tasks} required /></Field>
        </div>
        <Field label="GitHub repo" hint="Optional. Fills in the gh command and the deploy instructions."><input class="input" name="repo" placeholder="sparkstonepdx/myapp" pattern="[A-Za-z0-9_.\-]+/[A-Za-z0-9_.\-]+" /></Field>
      </form>
    </Sheet>
  </>);
}

function SiteSheet(props: { app: string; site?: Site; onClose: () => void; after: (p?: Promise<unknown>) => void }) {
  // read the app and site before closing: closing clears them (props.app was "" by the time the URL was built)
  const op = (o: string, title: string) => { const app = props.app, s = props.site!; props.onClose(); props.after(runCmd(title, `/api/apps/${encodeURIComponent(app)}/sites/${encodeURIComponent(s.name)}/${o}`)); };
  const name = () => props.site?.host || props.site?.name || "";
  return (
    <Sheet open={!!props.site} title={name()} onClose={props.onClose}>
      <Show when={props.site}>
        {(s) => (
          <div class="stack">
            <p class="cmd-line">{siteBadge(s())} <span class="text-secondary">{kindTitle[s().kind]}</span></p>
            <Show when={s().host && !s().live}>
              <div class="note note-bad">Sync skipped this hostname: it isn't allowed for {props.app}, it's reserved, or another app has it. Sync shows which.</div>
            </Show>
            <dl class="facts">
              <Show when={s().kind === "cron"}><dt>Schedule</dt><dd class="mono">{s().schedule}</dd><dt>Next</dt><dd>{s().next && s().next !== "n/a" ? s().next : "not scheduled"}</dd><dt>Last run</dt><dd>{s().last && s().last !== "n/a" ? `${s().last} (${s().result})` : "never"}</dd></Show>
              <Show when={s().running}><dt>State</dt><dd>{s().ActiveState} · {s().SubState}</dd><Show when={s().MainPID && s().MainPID !== "0"}><dt>PID</dt><dd class="mono">{s().MainPID}</dd></Show><dt>Restarts</dt><dd>{s().NRestarts || 0}</dd></Show>
              <Show when={s().health}><dt>Health</dt><dd><span class="mono">{s().health}</span> · <Show when={s().healthy !== false} fallback={<span class="danger-text">{(s().healthError || "not answering").replace(`${s().health} `, "")}</span>}>{s().healthy ? "answering" : "not checked"}</Show></dd></Show>
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
            <Confirm class="btn btn-ghost danger remove" label="Remove site" ask={<>Remove {name()}? This deletes <span class="mono">sites/{s().name}</span>.</>} yes="Remove" onYes={() => op("remove", `Removed ${name()}`)} />
          </div>
        )}
      </Show>
    </Sheet>
  );
}

function ManageSheet(props: { app?: App; onClose: () => void; after: (p?: Promise<unknown>) => void; onLimits: (app: string) => void }) {
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
            <li><button class="row" onClick={() => props.onLimits(a().name)}><span class="row-main"><span>Limits</span><small>{a().memoryMax || "?"} memory · {cpuPct(a().cpu)} CPU</small></span><span class="row-end"><span class="chev">›</span></span></button></li>
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

// "1s" per second of CPU time = 100%: systemd reports CPUQuotaPerSecUSec as "500ms", "1s", "2s" or "infinity"
const cpuPct = (q: string) => {
  const m = /^([\d.]+)(ms|s)$/.exec(q || "");
  if (!m) return q === "infinity" ? "no limit" : q || "?";
  return `${Math.round(Number(m[1]) * (m[2] === "s" ? 100 : 0.1))}%`;
};

// An app's memory, CPU and process limits. The running slice changes at once; nothing restarts.
function LimitsSheet(props: { app?: App; onClose: () => void; after: (p?: Promise<unknown>) => void }) {
  return (
    <Sheet open={!!props.app} title={props.app ? `${props.app.name}'s limits` : ""} onClose={props.onClose}
      footer={<><button class="btn btn-secondary" onClick={props.onClose}>Cancel</button><button class="btn" type="submit" form="limits">Save</button></>}>
      <Show when={props.app}>
        {(a) => (
          <form id="limits" onSubmit={(e) => {
            e.preventDefault();
            const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>;
            const name = a().name;
            props.onClose();
            props.after(runCmd(`Changed ${name}'s limits`, `/api/apps/${name}/limits`, f));
          }}>
            <p class="hint">{mb(a().memory) ? `Using ${mb(a().memory)} now. ` : ""}Changes apply at once, without restarting it.</p>
            <div class="field-row">
              <Field label="Memory" hint="512M, 2G"><input class="input" name="memory" value={/^\d+[KMG]$/.test(a().memoryMax || "") ? a().memoryMax : ""} placeholder="keep" pattern="\d+[KMG]" /></Field>
              <Field label="CPU" hint="100% is one core"><input class="input" name="cpu" value={/^\d+%$/.test(cpuPct(a().cpu)) ? cpuPct(a().cpu) : ""} placeholder="keep" pattern="\d+%" /></Field>
            </div>
            <Field label="Processes and threads" hint="Empty fields keep what's set now."><input class="input" name="tasks" placeholder="keep" pattern="\d+" /></Field>
          </form>
        )}
      </Show>
    </Sheet>
  );
}
