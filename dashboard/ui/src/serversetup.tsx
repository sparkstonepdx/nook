import { For, Show, createResource, createSignal, onCleanup } from "solid-js";
import { api } from "./api";
import { ConfirmStrip, Field, Sheet, StatusBadge } from "./components";
import { ago } from "./format";

type HostKey = { type: string; fingerprint: string };
type Job = {
  host: string; port: number; user: string; move: boolean; fingerprint: string; step: string; log: string[];
  started: string; updated: string; done?: boolean; error?: string; invite?: string;
};

const steps = (move: boolean) => [
  ["connect", "Log in and add this server's key"],
  ["check", "Check it's Debian"],
  ["install", "Install nook"],
  ...(move ? [["move", "Move every app there"]] : []),
  ["finish", "Take this server's key back out"],
] as const;

const order = ["scan", "connect", "check", "install", "move", "finish"];

// The Servers part of the Server page: set a fresh VPS up from here over SSH, and the last setup's state.
export function ServerSetup() {
  const [job, { refetch }] = createResource(() => api<{ job: Job | null }>("/api/servers/setup").then((r) => r.job));
  const [open, setOpen] = createSignal(false);
  const timer = setInterval(() => { if (!document.hidden && (open() || (job() && !job()!.done))) refetch(); }, 2000);
  onCleanup(() => clearInterval(timer));
  const running = () => !!job() && !job()!.done;
  return (
    <div class="stack">
      <ul class="list">
        <Show when={job()} fallback={
          <li class="row">
            <span class="row-main"><span>Set up a new server</span><small>Give a fresh Debian VPS's address and its root login (or a user with passwordless sudo): nook installs itself there, and can move this server onto it.</small></span>
            <span class="row-end"><button class="btn btn-secondary btn-sm" onClick={() => setOpen(true)}>Set up</button></span>
          </li>
        }>
          {(j) => (
            <li class="row">
              <span class={`status-dot status-${j().error ? "tight" : j().done ? "ok" : "watch"}`} aria-hidden="true" />
              <span class="row-main">
                <span>{j().error ? `Setting up ${j().host} stopped` : j().done ? `${j().host} is set up${j().move ? " and this server moved there" : ""}` : `Setting up ${j().host}…`}</span>
                <small>{j().done ? `${ago(Date.parse(j().updated) / 1000)}` : (steps(j().move).find(([s]) => s === j().step)?.[1] ?? "Reading its host key")}</small>
              </span>
              <span class="row-end">
                <button class="btn btn-secondary btn-sm" onClick={() => setOpen(true)}>{running() ? "Follow" : "Details"}</button>
              </span>
            </li>
          )}
        </Show>
      </ul>
      <SetupSheet open={open()} job={job() ?? null} onClose={() => setOpen(false)} onStarted={refetch} />
    </div>
  );
}

function SetupSheet(props: { open: boolean; job: Job | null; onClose: () => void; onStarted: () => void }) {
  // a finished setup shows until you start another
  const [fresh, setFresh] = createSignal(false);
  const showJob = () => !!props.job && !(fresh() && props.job.done);
  return (
    <Sheet open={props.open} title={showJob() ? `Setting up ${props.job!.host}` : "Set up a new server"} onClose={() => { setFresh(false); props.onClose(); }} wide>
      <Show when={showJob()} fallback={<SetupForm onStarted={() => { setFresh(false); props.onStarted(); }} />}>
        <Progress job={props.job!} again={() => setFresh(true)} />
      </Show>
    </Sheet>
  );
}

function SetupForm(props: { onStarted: () => void }) {
  const [host, setHost] = createSignal("");
  const [port, setPort] = createSignal("22");
  const [user, setUser] = createSignal("root");
  const [by, setBy] = createSignal<"password" | "key">("password");
  const [secret, setSecret] = createSignal("");
  const [move, setMove] = createSignal(false);
  const [keys, setKeys] = createSignal<HostKey[] | null>(null);
  const [asking, setAsking] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const target = () => ({ host: host().trim(), port: port().trim() || "22", user: user().trim() || "root" });
  // the key to check: ED25519 when there is one, as providers show that first
  const key = () => keys()?.find((k) => k.type === "ED25519") ?? keys()?.[0];

  const scan = async (e: Event) => {
    e.preventDefault();
    setBusy(true); setError(""); setKeys(null);
    try { setKeys((await api<{ keys: HostKey[] }>("/api/servers/scan", target())).keys); }
    catch (err) { setError((err as Error).message); }
    setBusy(false);
  };
  const start = async () => {
    setBusy(true); setError("");
    try {
      await api("/api/servers/setup", { ...target(), fingerprint: key()!.fingerprint, move: move(), set: { [by()]: secret() } });
      setSecret("");
      props.onStarted();
    } catch (err) { setError((err as Error).message); }
    setBusy(false);
  };

  return (
    <form class="stack" onSubmit={scan}>
      <Field label="Address" hint="Its IP, or a hostname pointing at it">
        <input class="input mono" required placeholder="203.0.113.7" autocomplete="off" spellcheck={false} value={host()} onInput={(e) => { setHost(e.currentTarget.value); setKeys(null); }} />
      </Field>
      <div class="field-row">
        <Field label="SSH port"><input class="input mono" inputmode="numeric" value={port()} onInput={(e) => { setPort(e.currentTarget.value); setKeys(null); }} /></Field>
        <Field label="User"><input class="input mono" autocomplete="off" spellcheck={false} value={user()} onInput={(e) => setUser(e.currentTarget.value)} /></Field>
      </div>
      <div class="seg" role="group" aria-label="Log in with">
        <button type="button" aria-pressed={by() === "password"} classList={{ on: by() === "password" }} onClick={() => { setBy("password"); setSecret(""); }}>Password</button>
        <button type="button" aria-pressed={by() === "key"} classList={{ on: by() === "key" }} onClick={() => { setBy("key"); setSecret(""); }}>Private key</button>
      </div>
      <Show when={by() === "password"} fallback={
        <Field label="Private key" hint="The whole file, without a passphrase. Used once to add this server's own key, then forgotten.">
          <textarea class="textarea mono" rows={5} required autocomplete="off" spellcheck={false} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" value={secret()} onInput={(e) => setSecret(e.currentTarget.value)} />
        </Field>
      }>
        <Field label="Password" hint="Used once to add this server's own key, then forgotten.">
          <input class="input" type="password" required autocomplete="off" value={secret()} onInput={(e) => setSecret(e.currentTarget.value)} />
        </Field>
      </Show>
      <label class="switch-row">
        <input type="checkbox" role="switch" checked={move()} onChange={(e) => setMove(e.currentTarget.checked)} />
        <span><span>Move this server there</span><small>Every app, its data, users and settings, through a last backup. Off: only install nook there.</small></span>
      </label>

      <Show when={key()} fallback={
        <div class="actions-row"><button class="btn" disabled={busy()}>{busy() ? "Reaching it…" : "Check the server"}</button></div>
      }>
        {(k) => (
          <div class="stack">
            <div class="note">
              <span>Its {k().type} host key is <code class="mono fingerprint">{k().fingerprint}</code>. Check it matches the fingerprint your VPS provider shows (its console, or the welcome email). If it doesn't, stop: something else is answering at that address.</span>
            </div>
            <div class="actions-row">
              <Show when={asking()} fallback={
                <button type="button" class="btn" disabled={busy() || !secret()} onClick={() => move() ? setAsking(true) : start()}>{busy() ? "Starting…" : move() ? "It matches: set it up and move" : "It matches: set it up"}</button>
              }>
                <ConfirmStrip ask="Move every app there? This server's sites go down from its last backup until DNS points at the new one." yes="Move"
                  onNo={() => setAsking(false)} onYes={() => { setAsking(false); start(); }} />
              </Show>
            </div>
          </div>
        )}
      </Show>
      <Show when={error()}><div class="note note-bad" role="alert"><span>{error()}</span></div></Show>
    </form>
  );
}

function Progress(props: { job: Job; again: () => void }) {
  const at = () => order.indexOf(props.job.step);
  let pre!: HTMLPreElement;
  const stick = () => queueMicrotask(() => { if (pre) pre.scrollTop = pre.scrollHeight; });
  return (
    <div class="stack">
      <ul class="list">
        <For each={steps(props.job.move)}>
          {([s, label]) => {
            const i = order.indexOf(s);
            const state = () => (i < at() || (props.job.done && !props.job.error) ? "ok" : i === at() ? (props.job.error ? "tight" : "watch") : "learning");
            return (
              <li class="row">
                <span class={`status-dot status-${state()}`} aria-hidden="true" />
                <span class="row-main"><span>{label}</span></span>
                <Show when={i === at() && !props.job.done}><span class="row-end"><StatusBadge warn>now</StatusBadge></span></Show>
              </li>
            );
          }}
        </For>
      </ul>
      <Show when={props.job.error}><div class="note note-bad" role="alert"><span>{props.job.error}</span></div></Show>
      <Show when={props.job.done && props.job.move && !props.job.error}>
        <div class="note note-warn">
          <span>Moved. Point DNS for every domain this server served (the Domains page lists them) at {props.job.host}. This server keeps its apps stopped until you retire it.</span>
        </div>
      </Show>
      <Show when={props.job.invite}>
        <div class="note note-warn">
          <span>nook is installed on {props.job.host}. Open its dashboard to make its admin login (one use, 24 hours). Its certificate is self-signed until you set its address, so accept the warning.</span>
        </div>
        <div class="actions-row"><a class="btn" href={props.job.invite} target="_blank" rel="noopener">Open {props.job.host}'s dashboard</a></div>
      </Show>
      <details open={!props.job.done || !!props.job.error}>
        <summary>What it printed</summary>
        <pre class="output log" ref={pre}>{(stick(), props.job.log.join("\n") || "(nothing yet)")}</pre>
      </details>
      <Show when={props.job.done}>
        <div class="actions-row"><button class="btn btn-secondary" onClick={props.again}>Set up another</button></div>
      </Show>
    </div>
  );
}
