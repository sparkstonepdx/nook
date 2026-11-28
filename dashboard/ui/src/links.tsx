import { For, Show, createResource, createRoot, createSignal } from "solid-js";
import { api, runCmd, say, server, switchServer, type CmdResult } from "./api";
import { ConfirmStrip, Field, Sheet, TokenBox } from "./components";
import { ago } from "./format";

export type Link = { id: string; name: string; url: string; kind: string; since: number; seen?: number };
type LinkInfo = { self?: { id: string; name: string }; error?: string; links: Link[] };

// This server and the servers linked with it, loaded once per page.
export const [links, { refetch: refetchLinks }] = createRoot(() => createResource(async () => {
  const i = await api<LinkInfo>("/api/links", undefined, "");
  // acting on a server that isn't linked any more: back to this one
  if (server && !i.links.some((l) => l.id === server)) switchServer("");
  return i;
}));

/** Every server: this one first, then the linked ones. */
export const servers = () => {
  const i = links();
  if (!i?.self) return [];
  return [{ id: i.self.id, name: i.self.name, here: true }, ...i.links.map((l) => ({ id: l.id, name: l.name, here: false }))];
};
/** The server being acted on. */
export const current = () => servers().find((s) => (server ? s.id === server : s.here));

// In the top bar, once there's more than one server: which one the dashboard acts on.
export function ServerPick() {
  return (
    <Show when={servers().length > 1}>
      <label class="server-pick">
        <select class="select" aria-label="Server to act on" value={current()?.id ?? ""} onChange={(e) => {
          const s = servers().find((x) => x.id === e.currentTarget.value);
          switchServer(s && !s.here ? s.id : "");
        }}>
          <For each={servers()}>{(s) => <option value={s.id}>{s.name}{s.here ? " (this one)" : ""}</option>}</For>
        </select>
      </label>
    </Show>
  );
}

// The Apps page's apps on the other servers: one line each, opening it there.
export function OtherServersApps() {
  const others = () => servers().filter((s) => s.id !== current()?.id);
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

// On the Server page: the linked servers, linking another, and unlinking.
export function LinkedServers() {
  const [open, setOpen] = createSignal(false);
  const [asking, setAsking] = createSignal("");
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
                    <small>Linked {ago(l.since)}{l.seen ? ` · last heard from ${ago(l.seen)}` : ""} · <span class="mono">{l.id}</span></small>
                  </span>
                  <span class="row-end">
                    <Show when={asking() === l.id} fallback={<button class="btn btn-ghost btn-sm" onClick={() => setAsking(l.id)}>Unlink</button>}>
                      <ConfirmStrip ask={`Unlink ${l.name}? Neither can act on the other after; logins stay as they are on both.`} yes="Unlink"
                        onNo={() => setAsking("")} onYes={async () => { setAsking(""); await runCmd(`Unlinked ${l.name}`, `/api/links/${l.id}/remove`, {}); refetchLinks(); }} />
                    </Show>
                  </span>
                </li>
              )}
            </For>
            <li class="row">
              <span class="row-main"><span>Link a server</span><small>Act on another nook server of yours from here, with your logins kept the same on both.</small></span>
              <span class="row-end"><button class="btn btn-secondary btn-sm" disabled={!!i().error} title={i().error} onClick={() => setOpen(true)}>Link</button></span>
            </li>
          </ul>
        )}
      </Show>
      <Show when={links()?.error}><p class="hint">{links()!.error}</p></Show>
      <LinkSheet open={open()} onClose={() => setOpen(false)} />
    </div>
  );
}

function LinkSheet(props: { open: boolean; onClose: () => void }) {
  const [code, setCode] = createSignal("");
  const [mine, setMine] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const close = () => { setCode(""); setMine(""); setError(""); props.onClose(); };
  return (
    <Sheet open={props.open} title="Link a server" onClose={close}>
      <div class="stack">
        <form class="stack" onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true); setError("");
          try {
            const r = await api<CmdResult>("/api/links/join", { code: code().trim() }, "");
            if (!r.ok) setError(r.out.trim());
            else { say(r.out.trim() || "Linked", { kind: "success" }); refetchLinks(); close(); }
          } catch (err) { setError((err as Error).message); }
          setBusy(false);
        }}>
          <Field label="The other server's link code" hint="On that server: Server page, Servers, Link, then Make a code.">
            <input class="input mono" required autocomplete="off" spellcheck={false} placeholder="nooklink_admin.example.com/…" value={code()} onInput={(e) => setCode(e.currentTarget.value)} />
          </Field>
          <Show when={error()}><div class="note note-bad" role="alert"><span>{error()}</span></div></Show>
          <div class="actions-row"><button class="btn" disabled={busy() || !code().trim()}>{busy() ? "Linking…" : "Link"}</button></div>
        </form>
        <div class="chart-head"><span class="eyebrow">Or link from the other server</span></div>
        <Show when={mine()} fallback={
          <div class="actions-row"><button class="btn btn-secondary" onClick={async () => {
            try { setMine((await api<{ code: string }>("/api/links/code", { kind: "same" }, "")).code); }
            catch (err) { setError((err as Error).message); }
          }}>Make a code for this server</button></div>
        }>
          <div class="note note-warn"><span>Paste this on the other server within 24 hours. It works once. Keep it private: whoever has it can act on this server.</span></div>
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
    <Show when={servers().length > 1}>
      <ul class="list">
        <li><button class="row" onClick={() => setOpen(true)}><span class="row-main"><span>Move to another server</span><small>Its folder, data and settings, through a backup both servers use</small></span><span class="row-end"><span class="chev">›</span></span></button></li>
      </ul>
      <MoveSheet app={props.app} open={open()} onClose={() => setOpen(false)} onMoved={props.onMoved} />
    </Show>
  );
}

function MoveSheet(props: { app: string; open: boolean; onClose: () => void; onMoved: () => void }) {
  const targets = () => servers().filter((s) => s.id !== current()?.id);
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
          <p class="hint">{props.app} stops here, is backed up, and starts on the other server from that backup, with its settings. Both servers need to back up to the same repository. An app using or providing a service (a database) can't move this way yet.</p>
          <Field label="To">
            <select class="select" value={target()?.id} onChange={(e) => setTo(e.currentTarget.value)}>
              <For each={targets()}>{(s) => <option value={s.id}>{s.name}{s.here ? " (this one)" : ""}</option>}</For>
            </select>
          </Field>
          <div class="actions-row">
            <Show when={asking()} fallback={<button class="btn" disabled={busy() || !target()} onClick={() => setAsking(true)}>{busy() ? "Moving…" : `Move to ${target()?.name ?? ""}`}</button>}>
              <ConfirmStrip ask={`Move ${props.app}? Its sites are down until it's started there and DNS points at ${target()?.name}. The copy here is deleted once it's running there.`} yes="Move"
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
