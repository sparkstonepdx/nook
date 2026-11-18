import { For, Show, createEffect, createResource, createSignal, on } from "solid-js";
import { A, useLocation, useNavigate, useParams, useSearchParams } from "@solidjs/router";
import { api, attempt, say } from "../api";
import { Field, PageHead, Sheet, StatusBadge, Confirm } from "../components";
import { JobProgress, jobFor, jobLine, jobs, pollJobs, running } from "../storejobs";

// Requests this page has in flight, so polling runs every second from the click, before the job file exists.
const [busy, setBusy] = createSignal(0);
type Phase = "form" | "running" | "done" | "failed";

type EnvField = { key: string; desc?: string; default?: string; secret?: boolean; required?: boolean };
type Image = { path: string; sha256: string; alt?: string; width?: number; height?: number };
type CatalogApp = { versions?: string[]; apt?: { packages: string[] }; id: string; name: string; description: string; homepage?: string; repo?: string; license?: string; version: string; kind?: string; site?: string; memory?: string; needs?: string[]; ports?: number[]; env?: EnvField[]; icon?: Image; screenshots?: Image[]; artifacts?: Record<string, unknown>; catalog: string; catalogName: string; official?: boolean };
type Row = { app: string; id: string; name: string; version: string; latest: string; available: boolean; site: string; policy: string; url?: string; lastError?: string; catalog?: string; catalogName: string; official: boolean };
type CatalogInfo = { id: string; name: string; repo?: string; url: string; official: boolean; fingerprint: string; apps: number; fetchedAt?: string; error?: string };
type View = { catalog: CatalogApp[]; catalogs: CatalogInfo[]; fetchedAt: string; error?: string; installed: Row[]; taken?: string[] };

// Where an app comes from: a green check for the official catalog, the catalog's name for any other.
export function Origin(props: { official?: boolean; name: string }) {
  return (
    <Show when={props.official} fallback={<span class="badge badge-warning" title={`From the community catalog ${props.name}. nook doesn't review it.`}>{props.name}</span>}>
      <span class="badge badge-success verified" title="From the official nook catalog"><span aria-hidden="true">✓</span> Verified</span>
    </Show>
  );
}

// Catalog images come through the helper, which checks them against the hash the signed index pins.
const imageURL = (a: CatalogApp, i: Image) => `/api/store/image/${encodeURIComponent(a.catalog)}/${i.sha256}`;
const appPath = (a: CatalogApp) => `/store/apps/${encodeURIComponent(a.catalog)}/${encodeURIComponent(a.id)}`;

function AppIcon(props: { app: CatalogApp; big?: boolean }) {
  return (
    <Show when={props.app.icon} fallback={<span class="app-coin" classList={{ big: props.big }} aria-hidden="true">{props.app.name.slice(0, 1)}</span>}>
      {(i) => <img class="app-coin" classList={{ big: props.big }} src={imageURL(props.app, i())} alt="" width={props.big ? 64 : 44} height={props.big ? 64 : 44} />}
    </Show>
  );
}

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
  const installedIDs = () => new Set((data()?.installed ?? []).map((r) => `${r.catalog || "official"}/${r.id}`));
  const loc = useLocation();
  const browsing = () => loc.pathname.startsWith("/store/install");
  const route = useParams();
  const detail = () => loc.pathname.startsWith("/store/apps/") ? data()?.catalog.find((a) => a.catalog === route.catalog && a.id === route.id) : undefined;
  const managing = () => loc.pathname.startsWith("/store/catalogs");
  const [adding, setAdding] = createSignal(false);
  const navigate = useNavigate();
  const row = () => data()?.installed.find((r) => r.app === open());
  const pollNow = pollJobs(() => busy() > 0);
  // a job that just finished changes versions and the list: read it again
  createEffect(on(() => jobs().filter((j) => j.done).map((j) => j.app + j.updated).join(), (k, prev) => { if (prev !== undefined && k !== prev) refetch(); }));
  const installing = () => jobs().filter((j) => j.action === "install" && (running(j) || (j.error && !data()?.installed.some((r) => r.app === j.app))));

  // A storefront's "Install on nook" lands here: /store/install?app=ID&catalog=official, or &source=owner/repo
  // for a community catalog, which is offered for adding first.
  const [params, setParams] = useSearchParams();
  const [offer, setOffer] = createSignal<string | null>(null);
  let handled = false;
  createEffect(() => {
    const d = data(), app = params.app as string | undefined;
    if (!d || !app || handled) return;
    handled = true;
    const source = params.source as string | undefined;
    const cat = source ? d.catalogs.find((c) => c.repo?.toLowerCase() === source.toLowerCase() || c.url === source || c.url === `${source.replace(/\/$/, "")}/`) : d.catalogs.find((c) => c.official);
    if (!cat) { setOffer(source!); return; }
    const a = d.catalog.find((x) => x.catalog === cat.id && x.id === app);
    if (a) setPicked(a);
    else say(`${app} isn't in ${cat.official ? "the official catalog" : cat.name}`, { kind: "error", detail: "Refresh the catalog, or check the link." });
    setParams({ app: undefined, catalog: undefined, source: undefined }, { replace: true });
  });
  const addOffered = async () => {
    const source = offer()!;
    setOffer(null);
    const ok = await attempt("Couldn't add that catalog", async () => {
      const r = await api<{ name: string; fingerprint: string }>("/api/store/catalogs", { source });
      say(`Added ${r.name}`, { kind: "success", detail: `Its key is ${r.fingerprint}.` });
      handled = false;
      setRefresh(true); refetch();
    });
    if (!ok) setParams({ app: undefined, source: undefined }, { replace: true });
  };

  return (
    <Show when={!loc.pathname.startsWith("/store/apps/")} fallback={
      <Show when={detail()} fallback={<Show when={data()}><PageHead title="Not in your catalogs" back={{ href: "/store/install", label: "Install" }} /><p class="lede">Refresh the catalog, or check the link.</p></Show>}>
        {(a) => <>
          <AppDetail app={a()} installed={installedIDs().has(`${a().catalog}/${a().id}`)} onInstall={() => setPicked(a())} />
          <InstallSheet app={picked()} onClose={() => setPicked(null)} onDone={() => { refetch(); navigate("/store"); }} taken={installedIDs()} used={new Set(data()?.taken ?? [])} poll={pollNow} />
        </>}
      </Show>
    }>
    <>
      <PageHead title="Store">
        <button class="btn btn-ghost btn-sm" onClick={() => { setRefresh(true); refetch(); }}>Refresh catalog</button>
      </PageHead>
      <p class="lede">Each app installs as its own nook app, with its own user, folder and limits. Updates roll back by themselves if the new version doesn't come up.</p>
      <Show when={data.error || data()?.error}><div class="note note-bad">{String(data.error?.message ?? data()?.error)}</div></Show>

      <nav class="tabs" role="tablist">
        <A href="/store" end class="tab" activeClass="tab-active" role="tab">Installed<Show when={data()?.installed.length}> ({data()!.installed.length})</Show></A>
        <A href="/store/install" class="tab" activeClass="tab-active" role="tab">Install</A>
        <A href="/store/catalogs" class="tab" activeClass="tab-active" role="tab">Catalogs</A>
      </nav>

      <Show when={!browsing() && !managing()}>
        <Show when={installing().length}>
          <ul class="list">
            <For each={installing()}>
              {(j) => (
                <li class="row job-row">
                  <span class="row-main"><span>{j.app}</span><small>{j.name} <span class="mono">{j.to}</span></small></span>
                  <span class="row-end"><Show when={j.error} fallback={<StatusBadge warn>{jobLine(j)}</StatusBadge>}><StatusBadge ok={false} title={j.error}>install failed</StatusBadge></Show></span>
                </li>
              )}
            </For>
          </ul>
        </Show>
        <Show when={data()?.installed.length} fallback={<Show when={data() && !installing().length}><p class="empty">Nothing from the store yet. <A class="link" href="/store/install">Install an app</A></p></Show>}>
          <ul class="list">
            <For each={data()!.installed}>
              {(r) => (
                <li><button class="row" onClick={() => setOpen(r.app)}>
                  <span class="row-main"><span>{r.app}</span><small>{r.name} <span class="mono">{r.version}</span> · updates: {policyLabel[r.policy]?.toLowerCase()}</small></span>
                  <span class="row-end">
                    <Show when={running(jobFor(r.app))} fallback={<>
                      <Origin official={r.official} name={r.catalogName} />
                      <Show when={r.lastError}><StatusBadge ok={false} title={r.lastError}>rolled back</StatusBadge></Show>
                      <Show when={r.available}><StatusBadge warn>{r.latest}</StatusBadge></Show>
                    </>}><StatusBadge warn>{jobLine(jobFor(r.app)!)}</StatusBadge></Show>
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
              <li><A class="row" href={appPath(a)}>
                <AppIcon app={a} />
                <span class="row-main">
                  <span>{a.name} <Origin official={a.official} name={a.catalogName} /></span>
                  <small>{a.description}</small>
                </span>
                <span class="row-end"><Show when={installedIDs().has(`${a.catalog}/${a.id}`)}><span class="badge">Installed</span></Show><span class="chev">›</span></span>
              </A></li>
            )}
          </For>
        </ul>
        <Show when={data()?.fetchedAt}><p class="hint">Catalogs checked {new Date(data()!.fetchedAt).toLocaleString()}.</p></Show>
      </Show>

      <Show when={managing()}>
        <div class="page-head sub-head">
          <p class="hint">Apps from the official catalog are checked by nook's maintainers and signed with nook's key. A community catalog is run by someone else: its apps are signed with its own key, pinned when you add it.</p>
          <div class="page-actions"><button class="btn" onClick={() => setAdding(true)}>Add a catalog</button></div>
        </div>
        <ul class="list">
          <For each={data()?.catalogs}>
            {(c) => (
              <li class="row">
                <span class="row-main">
                  <span>{c.official ? "Official catalog" : c.name} <Origin official={c.official} name="Community" /></span>
                  <small>{c.apps} {c.apps === 1 ? "app" : "apps"} · key <span class="mono">{c.fingerprint}</span><Show when={c.repo && !c.official}> · <a class="link" href={`https://github.com/${c.repo}`} target="_blank" rel="noopener">{c.repo}</a></Show></small>
                  <Show when={c.error}><small class="text-error">{c.error}</small></Show>
                </span>
                <span class="row-end">
                  <Show when={!c.official}>
                    <Confirm label="Remove" yes="Remove" ask={(() => {
                      const using = (data()?.installed ?? []).filter((r) => r.catalog === c.id).map((r) => r.app);
                      return `Remove ${c.name}?${using.length ? ` ${using.join(", ")} keep running but stop getting updates.` : ""}`;
                    })()} onYes={async () => {
                      await attempt("Couldn't remove it", async () => { await api(`/api/store/catalogs/${encodeURIComponent(c.id)}/remove`, {}); say(`Removed ${c.name}`, { kind: "success" }); });
                      refetch();
                    }} />
                  </Show>
                </span>
              </li>
            )}
          </For>
        </ul>
      </Show>

      <Sheet open={adding()} title="Add a catalog" onClose={() => setAdding(false)}
        footer={<><button class="btn btn-secondary" onClick={() => setAdding(false)}>Cancel</button><button class="btn" type="submit" form="add-catalog">Add</button></>}>
        <form id="add-catalog" onSubmit={async (e) => {
          e.preventDefault();
          const source = String(new FormData(e.currentTarget).get("source")).trim();
          setAdding(false);
          await attempt("Couldn't add that catalog", async () => {
            const r = await api<{ name: string; fingerprint: string }>("/api/store/catalogs", { source });
            say(`Added ${r.name}`, { kind: "success", detail: `Its key is ${r.fingerprint}. Check that with whoever runs it.` });
            setRefresh(true); refetch();
          });
        }}>
          <div class="note note-warn">nook doesn't review community catalogs. An app from one runs as its own user like any other, but only add catalogs from people you trust.</div>
          <Field label="GitHub repo or URL" hint="A repo publishes its catalog to its dist branch. A URL is where index.json, index.json.sig and catalog.pub are."><input class="input mono" name="source" required placeholder="owner/nook-catalog" autocomplete="off" /></Field>
        </form>
      </Sheet>

      <Sheet open={!!offer()} title="Add a community catalog?" onClose={() => { setOffer(null); setParams({ app: undefined, source: undefined }, { replace: true }); }}
        footer={<><button class="btn btn-secondary" onClick={() => { setOffer(null); setParams({ app: undefined, source: undefined }, { replace: true }); }}>Cancel</button><button class="btn" onClick={addOffered}>Add and continue</button></>}>
        <div class="stack">
          <p><strong class="mono">{offer()}</strong> isn't one of your catalogs yet. Adding it pins its signing key, then opens the install.</p>
          <div class="note note-warn"><span>It's a community catalog, not the official one. nook doesn't review it, so add it only if you trust whoever runs it.</span></div>
        </div>
      </Sheet>
      <InstallSheet app={picked()} onClose={() => setPicked(null)} onDone={() => { refetch(); navigate("/store"); }} taken={installedIDs()} used={new Set(data()?.taken ?? [])} poll={pollNow} />
      <InstalledSheet row={row()} onClose={() => setOpen(null)} onDone={() => refetch()} poll={pollNow} />
    </>
    </Show>
  );
}

const kindLabel: Record<string, string> = { web: "A website at its own address", service: "A service other apps use", cron: "Runs on a schedule", runtime: "Something other apps run, like a language" };

function AppDetail(props: { app: CatalogApp; installed: boolean; onInstall: () => void }) {
  const a = () => props.app;
  const arches = () => Object.keys(a().artifacts ?? {}).join(", ");
  return (
    <>
      <PageHead title={<span class="app-title"><AppIcon app={a()} big />{a().name}</span>} back={{ href: "/store/install", label: "Install" }}>
        <button class="btn" onClick={props.onInstall}>{props.installed ? "Add another" : "Install"}</button>
      </PageHead>
      <p class="cmd-line"><Origin official={a().official} name={a().catalogName} /> <span class="mono text-secondary">{a().version}</span></p>
      <p class="lede">{a().description}</p>
      <Show when={a().screenshots?.length}>
        <ul class="gallery" aria-label="Screenshots">
          <For each={a().screenshots}>
            {(i) => <li><a href={imageURL(a(), i)} target="_blank" rel="noopener"><img src={imageURL(a(), i)} alt={i.alt ?? ""} width={i.width} height={i.height} style={i.width && i.height ? { "--ar": `${i.width} / ${i.height}` } : undefined} loading="lazy" /></a></li>}
          </For>
        </ul>
      </Show>
      <section class="section">
        <h2>Details</h2>
        <dl class="facts card">
          <dt>Kind</dt><dd>{kindLabel[a().kind || "web"]}</dd>
          <Show when={a().license}><dt>License</dt><dd>{a().license}</dd></Show>
          <Show when={a().memory}><dt>Memory</dt><dd>up to {a().memory}</dd></Show>
          <Show when={arches()}><dt>Runs on</dt><dd>{arches()}</dd></Show>
          <Show when={a().needs?.length}><dt>Needs</dt><dd>{a().needs!.join(", ")}</dd></Show>
          <Show when={a().ports?.length}><dt>Public ports</dt><dd>{a().ports!.join(", ")}: it listens on these itself, and installing it allows that</dd></Show>
        </dl>
      </section>
      <Show when={a().env?.length}>
        <section class="section">
          <h2>It asks for</h2>
          <ul class="list">
            <For each={a().env}>{(f) => <li class="row"><span class="row-main"><span class="mono">{f.key}</span><small>{f.desc}<Show when={f.default}> · default <span class="mono">{f.default}</span></Show></small></span></li>}</For>
          </ul>
        </section>
      </Show>
      <Show when={a().homepage || a().repo}>
        <section class="section">
          <h2>The original</h2>
          <ul class="list">
            <Show when={a().homepage}><li><a class="row" href={a().homepage} target="_blank" rel="noopener"><span class="row-main"><span>Homepage</span><small class="mono">{a().homepage!.replace(/^https:\/\//, "")}</small></span><span class="row-end"><span class="chev">↗</span></span></a></li></Show>
            <Show when={a().repo}><li><a class="row" href={a().repo} target="_blank" rel="noopener"><span class="row-main"><span>Source code</span><small class="mono">{a().repo!.replace(/^https:\/\//, "")}</small></span><span class="row-end"><span class="chev">↗</span></span></a></li></Show>
          </ul>
        </section>
      </Show>
    </>
  );
}

// run starts a store request with its progress in the sheet; the request's own output shows if it fails.
async function run(path: string, body: unknown, poll: () => void, setPhase: (p: Phase) => void, setOut: (s: string) => void) {
  setPhase("running");
  setOut("");
  setBusy((n) => n + 1);
  poll();
  let r: { ok: boolean; out: string };
  try { r = await api<{ ok: boolean; out: string }>(path, body); } catch (e) { r = { ok: false, out: (e as Error).message }; }
  setBusy((n) => n - 1);
  poll();
  setOut(r.out);
  setPhase(r.ok ? "done" : "failed");
  return r.ok;
}

function InstalledSheet(props: { row?: Row; onClose: () => void; onDone: () => void; poll: () => void }) {
  const [phase, setPhaseNow] = createSignal<Phase>("form");
  const [out, setOutNow] = createSignal("");
  const [target, setTarget] = createSignal("");
  let session = 0; // a run started before the sheet was closed doesn't write into the next one
  const guard = <T,>(f: (v: T) => void) => { const s = session; return (v: T) => { if (s === session) f(v); }; };
  // an update the daily check started, or one started before the sheet was closed, shows here too, and stays
  // shown once it finishes until the sheet closes
  const job = () => props.row && jobFor(props.row.app);
  const [watching, setWatching] = createSignal(false);
  createEffect(() => { if (running(job())) setWatching(true); });
  const close = () => { session++; setPhaseNow("form"); setWatching(false); props.onClose(); };
  const showing = () => phase() !== "form" || watching();
  const ended = () => phase() === "done" || (phase() === "form" && job()?.done && !job()?.error);
  return (
    <Sheet open={!!props.row} title={props.row?.app ?? ""} onClose={close}
      footer={<Show when={showing()}><button class="btn btn-secondary" onClick={close}>{phase() === "running" || running(job()) ? "Hide" : "Close"}</button></Show>}>
      <Show when={props.row}>
        {(r) => (
          <Show when={!showing()} fallback={
            <div class="stack">
              <p class="update-versions"><span class="mono">{job()?.from ?? r().version}</span> <span aria-hidden="true">→</span> <strong class="mono">{job()?.to ?? target()}</strong></p>
              <JobProgress job={job()?.to === (target() || job()?.to) ? job() : undefined} />
              <Show when={ended()}><p class="note">{r().app} is on {job()?.to ?? target()}.</p></Show>
              <Show when={phase() === "failed" && out()}><pre class="output">{out()}</pre></Show>
              <Show when={running(job()) || phase() === "running"}><p class="hint">You can close this; it keeps going, and the Store list shows where it is.</p></Show>
            </div>
          }>
          <div class="stack">
            <dl class="facts">
              <dt>App</dt><dd>{r().name}</dd>
              <dt>From</dt><dd><Origin official={r().official} name={r().catalogName} /></dd>
              <dt>Version</dt><dd class="mono">{r().version}<Show when={r().available}> · {r().latest} available</Show></dd>
              <dt>Site</dt><dd><Show when={r().url} fallback={<span class="mono">{r().site}</span>}><a class="link" href={r().url} target="_blank" rel="noopener">{r().site}</a></Show></dd>
            </dl>
            <Show when={r().lastError}><div class="note note-bad">The last update didn't come up, so it was rolled back: {r().lastError}</div></Show>
            <Field label="Updates" hint={policyHint[r().policy]}>
              <select class="select" onChange={async (e) => {
                const p = e.currentTarget.value;
                await attempt("Couldn't change updates", async () => { await api(`/api/store/${r().app}/policy`, { policy: p }); say(`${r().app}: ${policyLabel[p].toLowerCase()}`, { kind: "success" }); });
                props.onDone();
              }}>
                <For each={Object.keys(policyLabel)}>{(p) => <option value={p} selected={p === r().policy}>{policyLabel[p]}</option>}</For>
              </select>
            </Field>
            <div class="actions-grid">
              <Show when={r().available}>
                <button class="btn" onClick={async () => { const a = r().app; setTarget(r().latest); await run(`/api/store/${a}/update`, {}, props.poll, guard(setPhaseNow), guard(setOutNow)); props.onDone(); }}>Update to {r().latest}</button>
              </Show>
              <A class="btn btn-secondary" href={`/apps/${r().app}/env`}>Settings</A>
            </div>
          </div>
          </Show>
        )}
      </Show>
    </Sheet>
  );
}

function InstallSheet(props: { app: CatalogApp | null; onClose: () => void; onDone: () => void; taken: Set<string>; used: Set<string>; poll: () => void }) {
  const [phase, setPhaseNow] = createSignal<Phase>("form");
  const [out, setOutNow] = createSignal("");
  const [name, setName] = createSignal("");
  let session = 0;
  const guard = <T,>(f: (v: T) => void) => { const s = session; return (v: T) => { if (s === session) f(v); }; };
  const finished = () => phase() === "done" || phase() === "failed";
  const close = () => { session++; const ok = phase() === "done"; setPhaseNow("form"); props.onClose(); if (ok) props.onDone(); };
  const [major, setMajor] = createSignal("");
  const pick = () => major() || (props.app?.versions ?? []).slice(-1)[0] || "";
  const job = () => { const j = jobFor(name()); return j && j.action === "install" && (j.to === props.app?.version || j.to === pick()) ? j : undefined; };
  // an app with majors installs each as its own app: postgres-17
  // another copy of an app (a second PocketBase) gets the first number free for both its app and its site:
  // pocketbase-2 at pocketbase-2.<domain>, then -3, and so on
  const copyNo = (a: CatalogApp) => {
    const site = a.site ?? a.id;
    for (let n = 1; ; n++) {
      const sfx = n === 1 ? "" : `-${n}`;
      const free = !props.used.has(`app:${a.id}${sfx}`) && !props.used.has(`site:${site}${sfx}`) && (n > 1 || !props.taken.has(`${a.catalog}/${a.id}`));
      if (free) return sfx;
    }
  };
  const defaultName = (a: CatalogApp) => (a.versions?.length ? `${a.id}-${pick().replace(".", "-")}` : `${a.id}${copyNo(a)}`);
  const defaultSite = (a: CatalogApp) => (a.versions?.length ? defaultName(a) : `${a.site ?? a.id}${copyNo(a)}`);
  return (
    <Sheet open={!!props.app} title={props.app ? (phase() === "failed" ? `${props.app.name} didn't install` : `Install ${props.app.name}`) : ""} onClose={close}
      footer={<Show when={phase() === "form"} fallback={<button type="button" class="btn btn-secondary" onClick={close}>{finished() ? "Close" : "Hide"}</button>}>
        <button type="button" class="btn btn-secondary" onClick={close}>Cancel</button><button class="btn" type="submit" form="store-install">Install</button>
      </Show>}>
      <Show when={props.app}>
        {(a) => (
          <Show when={phase() === "form"} fallback={
            <div class="stack">
              <p class="update-versions"><strong>{name()}</strong> <span class="mono">{a().version}</span></p>
              <JobProgress job={job()} />
              <Show when={phase() === "done"}><p class="note">Installed. It's on the Installed tab, and on the Apps page as {name()}.</p></Show>
              <Show when={phase() === "failed" && out()}><pre class="output">{out()}</pre></Show>
              <Show when={phase() === "running"}><p class="hint">You can close this; it keeps going, and the Store list shows where it is.</p></Show>
            </div>
          }>
          <form id="store-install" onSubmit={async (e) => {
            e.preventDefault();
            const app = a();
            const f = new FormData(e.currentTarget);
            const env: Record<string, string> = {};
            for (const fld of app.env ?? []) env[fld.key] = String(f.get(`env.${fld.key}`) ?? "");
            const body = { catalog: app.catalog, id: app.id, app: String(f.get("app")), site: String(f.get("site")), policy: String(f.get("policy")), version: app.versions?.length ? pick() : "", set: env };
            setName(body.app);
            await run("/api/store/install", body, props.poll, guard(setPhaseNow), guard(setOutNow));
          }}>
            <Show when={a().official} fallback={
              <div class="note note-warn"><span>From the community catalog <strong>{a().catalogName}</strong>, not the official one. nook doesn't review it, so install it only if you trust whoever runs that catalog.</span></div>
            }>
              <p class="cmd-line"><Origin official name="" /> <span class="hint">From the official nook catalog.</span></p>
            </Show>
            <p class="hint">{a().description}<Show when={!a().versions?.length}> Version <span class="mono">{a().version}</span>.</Show></p>
            <Show when={a().versions?.length}>
              <Field label="Version" hint="Each version runs as its own app, so two can sit side by side. Updates within it come from apt.">
                <select class="select" onChange={(e) => setMajor(e.currentTarget.value)}><For each={[...(a().versions ?? [])].reverse()}>{(v) => <option value={v} selected={v === pick()}>{a().name} {v}</option>}</For></select>
              </Field>
            </Show>
            <div class="field-row">
              <Field label="App name"><input class="input mono" name="app" value={defaultName(a())} pattern="[a-z][a-z0-9\-]{1,23}" required /></Field>
              <Field label="Site"><input class="input mono" name="site" value={defaultSite(a())} required /></Field>
            </div>
            <p class="hint">{a().kind === "cron" || a().kind === "service" || a().kind === "runtime" ? "It has no hostname." : "A subdomain, or a hostname the app is allowed."}</p>
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
          </Show>
        )}
      </Show>
    </Sheet>
  );
}
