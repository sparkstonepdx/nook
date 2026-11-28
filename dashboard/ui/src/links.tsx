import { For, Show, createResource, createRoot, createSignal } from "solid-js";
import { api, runCmd, say, server, switchServer, type CmdResult } from "./api";
import { ConfirmStrip, Field, Sheet, TokenBox } from "./components";
import { ago } from "./format";

export type Link = { id: string; name: string; url: string; kind: "same" | "manager" | "client"; role?: "operator" | "admin"; entity?: string; since: number; seen?: number };
type Srv = { id: string; name: string; here: boolean; entity: string; role?: string };
const YOURS = "Yours";
type LinkInfo = { self?: { id: string; name: string }; error?: string; links: Link[] };

// This server and the servers linked with it, loaded once per page.
export const [links, { refetch: refetchLinks }] = createRoot(() => createResource(async () => {
  const i = await api<LinkInfo>("/api/links", undefined, "");
  // acting on a server that isn't linked any more: back to this one
  if (server && !i.links.some((l) => l.id === server)) switchServer("");
  return i;
}));

/** Every server this dashboard can act on: this one first, your linked ones, then the ones it manages, each in
 * its entity (yours, or the client's). Servers that manage this one aren't among them. */
export const servers = (): Srv[] => {
  const i = links();
  if (!i?.self) return [];
  return [
    { id: i.self.id, name: i.self.name, here: true, entity: YOURS },
    ...i.links.filter((l) => l.kind === "same").map((l) => ({ id: l.id, name: l.name, here: false, entity: YOURS })),
    ...i.links.filter((l) => l.kind === "client").map((l) => ({ id: l.id, name: l.name, here: false, entity: l.entity || l.name, role: l.role })),
  ];
};
/** The server being acted on. */
export const current = () => servers().find((s) => (server ? s.id === server : s.here));
/** The servers in the same entity as the one being acted on: nothing crosses entities. */
export const sameEntity = () => servers().filter((s) => s.entity === (current()?.entity ?? YOURS));
const entities = () => [...new Set(servers().map((s) => s.entity))];

// In the top bar, once there's more than one server: which one the dashboard acts on.
export function ServerPick() {
  return (
    <Show when={servers().length > 1}>
      <label class="server-pick">
        <select class="select" aria-label="Server to act on" value={current()?.id ?? ""} onChange={(e) => {
          const s = servers().find((x) => x.id === e.currentTarget.value);
          switchServer(s && !s.here ? s.id : "");
        }}>
          <For each={entities()}>{(en) => (
            <optgroup label={en}>
              <For each={servers().filter((s) => s.entity === en)}>{(s) => <option value={s.id}>{s.name}{s.here ? " (this dashboard)" : ""}</option>}</For>
            </optgroup>
          )}</For>
        </select>
      </label>
    </Show>
  );
}

// The Apps page's apps on the other servers: one line each, opening it there.
export function OtherServersApps() {
  const others = () => sameEntity().filter((s) => s.id !== current()?.id);
  return (
    <For each={others()}>
      {(s) => {
        const [ov] = createResource(() => api<{ apps: { name: string; sites: unknown[] }[] }>("/api/overview", undefined, s.id));
        return (
          <section class="other-server">
            <div class="chart-head"><span class="eyebrow">On {s.name}</span></div>
            <Show when={!ov.error} fallback={<p class="hint text-error">Couldn't reach it: {String(ov.error?.message)}</p>}>
              <Show when={ov()?.apps.length} fallback={<Show when={ov()}><p class="empty">No apps there.</p></Show>}>
                <ul class="list">
                  <For each={ov()!.apps}>
                    {(a) => (
                      <li><button class="row" onClick={() => switchServer(s.here ? "" : s.id, "/")}>
                        <span class="row-main"><span class="app-name">{a.name}</span><small>{a.sites.length} {a.sites.length === 1 ? "site" : "sites"}</small></span>
                        <span class="row-end"><span class="chev">›</span></span>
                      </button></li>
                    )}
                  </For>
                </ul>
              </Show>
            </Show>
          </section>
        );
      }}
    </For>
  );
}

const roleText = { operator: "operator: deploys, store, env, domains, updates and logs", admin: "full admin, but never backups' settings" } as const;

function linkDetail(l: Link) {
  const seen = l.seen ? ` · last heard from ${ago(l.seen)}` : "";
  if (l.kind === "manager") return `Manages this server as ${roleText[l.role ?? "operator"]}${seen}`;
  if (l.kind === "client") return `${l.entity || l.name}'s server, managed from here as ${l.role}${seen}`;
  return `Yours, linked ${ago(l.since)}${seen}`;
}

// On the Server page: the linked servers (yours, the ones this one manages, the ones managing it), linking
// another, unlinking, and what managers did here.
export function LinkedServers() {
  const [open, setOpen] = createSignal(false);
  const [asking, setAsking] = createSignal("");
  const managed = () => !!links()?.links.some((l) => l.kind === "manager");
  return (
    <div class="stack">
      <Show when={links()}>
        {(i) => (
          <ul class="list">
            <For each={i().links}>
              {(l) => (
                <li class="row">
                  <span class={`status-dot status-${l.seen && Date.now() / 1000 - l.seen < 3600 ? "ok" : "learning"}`} aria-hidden="true" />
                  <span class="row-main">
                    <span>{l.name}</span>
                    <small>{linkDetail(l)}</small>
                  </span>
                  <span class="row-end">
                    <Show when={asking() === l.id} fallback={<button class="btn btn-ghost btn-sm" onClick={() => setAsking(l.id)}>Unlink</button>}>
                      <ConfirmStrip ask={l.kind === "manager" ? `Stop ${l.name} managing this server? Everything here keeps running.` : `Unlink ${l.name}? Neither can act on the other after.`} yes="Unlink"
                        onNo={() => setAsking("")} onYes={async () => { setAsking(""); await runCmd(`Unlinked ${l.name}`, `/api/links/${l.id}/remove`, {}); refetchLinks(); }} />
                    </Show>
                  </span>
                </li>
              )}
            </For>
            <li class="row">
              <span class="row-main"><span>Link a server</span><small>Another server of yours, a client's server to manage from here, or let someone manage this one.</small></span>
              <span class="row-end"><button class="btn btn-secondary btn-sm" disabled={!!i().error} title={i().error} onClick={() => setOpen(true)}>Link</button></span>
            </li>
          </ul>
        )}
      </Show>
      <Show when={links()?.error}><p class="hint">{links()!.error}</p></Show>
      <Show when={managed()}><ManagerAudit /></Show>
      <LinkSheet open={open()} onClose={() => setOpen(false)} />
    </div>
  );
}

type Audit = { t: number; server: string; by?: string; op: string; detail?: string; allowed: boolean };

// What the servers managing this one did here: changes and refusals, or everything.
function ManagerAudit() {
  const [all, setAll] = createSignal(false);
  const [list] = createResource(() => (all() ? "true" : "false"), (a) => api<{ entries: Audit[] }>(`/api/links/audit?all=${a}`, undefined, "").then((r) => r.entries));
  return (
    <details class="audit">
      <summary>What managers did here</summary>
      <div class="stack">
        <label class="switch-row">
          <input type="checkbox" role="switch" checked={all()} onChange={(e) => setAll(e.currentTarget.checked)} />
          <span><span>Show looking too</span><small>Reading logs, settings and status. Off: only changes and refusals.</small></span>
        </label>
        <Show when={list()?.length} fallback={<p class="empty">Nothing yet.</p>}>
          <ul class="list">
            <For each={list()}>
              {(e) => (
                <li class="row">
                  <span class={`status-dot status-${e.allowed ? "ok" : "tight"}`} aria-hidden="true" />
                  <span class="row-main">
                    <span><span class="mono">{e.op}</span>{e.allowed ? "" : " (refused)"}</span>
                    <small>{e.by ? `${e.by} at ` : ""}{e.server} · {ago(e.t)}{e.detail ? ` · ${e.detail}` : ""}</small>
                  </span>
                </li>
              )}
            </For>
          </ul>
        </Show>
      </div>
    </details>
  );
}

function LinkSheet(props: { open: boolean; onClose: () => void }) {
  const [code, setCode] = createSignal("");
  const [entity, setEntity] = createSignal("");
  const [kind, setKind] = createSignal<"same" | "managed">("same");
  const [role, setRole] = createSignal<"operator" | "admin">("operator");
  const [mine, setMine] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const close = () => { setCode(""); setEntity(""); setMine(""); setError(""); props.onClose(); };
  return (
    <Sheet open={props.open} title="Link a server" onClose={close}>
      <div class="stack">
        <form class="stack" onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true); setError("");
          try {
            const r = await api<CmdResult>("/api/links/join", { code: code().trim(), entity: entity().trim() }, "");
            if (!r.ok) setError(r.out.trim());
            else { say(r.out.trim() || "Linked", { kind: "success" }); refetchLinks(); close(); }
          } catch (err) { setError((err as Error).message); }
          setBusy(false);
        }}>
          <Field label="The other server's link code" hint="Made on that server: Server page, Servers, Link.">
            <input class="input mono" required autocomplete="off" spellcheck={false} placeholder="nooklink_admin.example.com/…" value={code()} onInput={(e) => setCode(e.currentTarget.value)} />
          </Field>
          <Field label="Client" hint="Only for a client's server you'll manage: its servers are grouped under this name, apart from yours.">
            <input class="input" autocomplete="off" placeholder="Second Chance Records" value={entity()} onInput={(e) => setEntity(e.currentTarget.value)} />
          </Field>
          <Show when={error()}><div class="note note-bad" role="alert"><span>{error()}</span></div></Show>
          <div class="actions-row"><button class="btn" disabled={busy() || !code().trim()}>{busy() ? "Linking…" : "Link"}</button></div>
        </form>
        <div class="chart-head"><span class="eyebrow">Or make a code here</span></div>
        <Show when={mine()} fallback={
          <div class="stack">
            <div class="seg" role="group" aria-label="Kind of link">
              <button type="button" aria-pressed={kind() === "same"} classList={{ on: kind() === "same" }} onClick={() => setKind("same")}>Another server of mine</button>
              <button type="button" aria-pressed={kind() === "managed"} classList={{ on: kind() === "managed" }} onClick={() => setKind("managed")}>Let someone manage this</button>
            </div>
            <Show when={kind() === "managed"} fallback={<p class="hint">Both servers act on each other, and your logins are kept the same on both.</p>}>
              <p class="hint">The server that takes the code manages this one from its own dashboard. Your logins stay yours, every request from it is listed here, backups stay where you set them, and you can unlink any time, even when it's unreachable.</p>
              <div class="seg" role="group" aria-label="What they may do">
                <button type="button" aria-pressed={role() === "operator"} classList={{ on: role() === "operator" }} onClick={() => setRole("operator")}>Operator</button>
                <button type="button" aria-pressed={role() === "admin"} classList={{ on: role() === "admin" }} onClick={() => setRole("admin")}>Full admin</button>
              </div>
              <p class="hint">{role() === "operator" ? "Deploys, the store, env, domains, system and nook updates, logs. Not deleting or moving apps, restores, ports, or restarting the server." : "Everything you can do here except backups' settings and moving apps off."} Never your users or apps' data.</p>
            </Show>
            <div class="actions-row"><button class="btn btn-secondary" onClick={async () => {
              try { setMine((await api<{ code: string }>("/api/links/code", { kind: kind(), role: kind() === "managed" ? role() : "" }, "")).code); }
              catch (err) { setError((err as Error).message); }
            }}>Make a code</button></div>
          </div>
        }>
          <div class="note note-warn"><span>Paste this on the other server within 24 hours. It works once. Keep it private: whoever has it can act on this server{kind() === "managed" ? ` as ${role() === "admin" ? "full admin" : "operator"}` : ""}.</span></div>
          <div class="field-row">
            <input class="input mono" readonly value={mine()} onFocus={(e) => e.currentTarget.select()} />
            <button type="button" class="btn btn-secondary" onClick={() => { navigator.clipboard?.writeText(mine()); say("Code copied"); }}>Copy</button>
          </div>
        </Show>
      </div>
    </Sheet>
  );
}

// In an app's Manage sheet: moving it to another linked server.
export function MoveRow(props: { app: string; onMoved: () => void }) {
  const [open, setOpen] = createSignal(false);
  return (
    <Show when={current()?.entity === YOURS && sameEntity().length > 1}>
      <ul class="list">
        <li><button class="row" onClick={() => setOpen(true)}><span class="row-main"><span>Move to another server</span><small>Its folder, data and settings, through a backup both servers use</small></span><span class="row-end"><span class="chev">›</span></span></button></li>
      </ul>
      <MoveSheet app={props.app} open={open()} onClose={() => setOpen(false)} onMoved={props.onMoved} />
    </Show>
  );
}

function MoveSheet(props: { app: string; open: boolean; onClose: () => void; onMoved: () => void }) {
  const targets = () => sameEntity().filter((s) => s.id !== current()?.id);
  const [to, setTo] = createSignal("");
  const [asking, setAsking] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [done, setDone] = createSignal<(CmdResult & { token?: string; server?: string }) | null>(null);
  const target = () => targets().find((s) => s.id === (to() || targets()[0]?.id));
  const close = () => { setDone(null); setAsking(false); props.onClose(); if (done()?.ok) props.onMoved(); };
  return (
    <Sheet open={props.open} title={`Move ${props.app}`} onClose={close} wide>
      <Show when={done()} fallback={
        <div class="stack">
          <p class="hint">{props.app} stops on {current()?.name}, is backed up, and starts on the other server from that backup, with its settings. Both servers need to back up to the same repository. An app using or providing a service (a database) can't move this way yet.</p>
          <Field label="To">
            <select class="select" value={target()?.id} onChange={(e) => setTo(e.currentTarget.value)}>
              <For each={targets()}>{(s) => <option value={s.id}>{s.name}{s.here ? " (this dashboard)" : ""}</option>}</For>
            </select>
          </Field>
          <div class="actions-row">
            <Show when={asking()} fallback={<button class="btn" disabled={busy() || !target()} onClick={() => setAsking(true)}>{busy() ? "Moving…" : `Move to ${target()?.name ?? ""}`}</button>}>
              <ConfirmStrip ask={`Move ${props.app}? Its sites are down until it's started there and DNS points at ${target()?.name}. The copy on ${current()?.name} is deleted once it's running there.`} yes="Move"
                onNo={() => setAsking(false)} onYes={async () => {
                  setAsking(false); setBusy(true);
                  try { setDone(await api(`/api/apps/${props.app}/move`, { to: target()!.here ? links()!.self!.id : target()!.id })); }
                  catch (err) { setDone({ ok: false, code: -1, cmd: "", out: (err as Error).message }); }
                  setBusy(false);
                }} />
            </Show>
          </div>
        </div>
      }>
        {(d) => (
          <div class="stack">
            <Show when={d().ok} fallback={<div class="note note-bad" role="alert"><span>{props.app} didn't move. What happened is below.</span></div>}>
              <div class="note note-warn"><span>{props.app} is running on {servers().find((s) => s.id === d().server)?.name}. Point its domains' DNS there, and give its repo this new deploy token (the old one named this server).</span></div>
              <Show when={d().token}><TokenBox token={d().token!} /></Show>
            </Show>
            <details open={!d().ok}><summary>What it printed</summary><pre class="output">{d().out}</pre></details>
            <div class="actions-row">
              <Show when={d().ok && d().server}><button class="btn btn-secondary" onClick={() => switchServer(d().server === links()?.self?.id ? "" : d().server!, "/")}>Open it there</button></Show>
              <button class="btn" onClick={close}>Close</button>
            </div>
          </div>
        )}
      </Show>
    </Sheet>
  );
}
