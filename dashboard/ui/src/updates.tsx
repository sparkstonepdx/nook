import { Show, createEffect, createResource, createSignal, onCleanup, onMount, type JSX } from "solid-js";
import { applyUpdate, updateReady } from "./update";
import { api, say } from "./api";
import { Sheet } from "./components";
import { Ansi } from "./ansi";

type UpdateStatus = { current: string; latest: string; available: boolean; url: string; notes: string; checkedAt: string; error?: string };

// Updating, in one place: the banner and the footer both open the update sheet.
const [updateOpen, setUpdateOpen] = createSignal(false);
const UPDATED_KEY = "nook-updated";

// After the reload that finishes an update, say it worked.
export function sayIfUpdated() {
  try {
    const v = sessionStorage.getItem(UPDATED_KEY);
    if (v) { sessionStorage.removeItem(UPDATED_KEY); say(`Updated to nook ${v}`, { kind: "success" }); }
  } catch { /* storage off */ }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// The running dashboard's version, straight from the server (not the update state), or "" while it's restarting.
async function runningVersion(): Promise<string> {
  try {
    const r = await fetch("/api/version", { cache: "no-store", credentials: "same-origin" });
    return r.ok ? ((await r.json()) as { version: string }).version : "";
  } catch { return ""; }
}

type Phase = "ready" | "installing" | "restarting" | "failed" | "slow";

function UpdateSheet(props: { s: () => UpdateStatus | null | undefined }) {
  const [phase, setPhase] = createSignal<Phase>("ready");
  const [out, setOut] = createSignal("");
  const busy = () => phase() === "installing" || phase() === "restarting";
  const close = () => { if (!busy()) { setUpdateOpen(false); setPhase("ready"); } };
  const run = async () => {
    const target = props.s()!.latest;
    setPhase("installing");
    // The update restarts this dashboard's own process, which usually cuts this request off: that's expected, not a
    // failure. A response that does come back with an error is a real one (download, signature, disk).
    try {
      const r = await api<{ ok: boolean; out: string }>("/api/update", {});
      if (!r.ok) { setOut(r.out); setPhase("failed"); return; }
    } catch { /* cut off by the restart */ }
    setPhase("restarting");
    const until = Date.now() + 120_000;
    while (Date.now() < until) {
      await sleep(1500);
      if ((await runningVersion()) === target) {
        try { sessionStorage.setItem(UPDATED_KEY, target); } catch { /* storage off */ }
        location.reload();
        return;
      }
    }
    setPhase("slow");
  };
  return (
    <Sheet open={updateOpen()} title={phase() === "failed" ? "The update didn't install" : `Update nook`} onClose={close}
      footer={
        <Show when={phase() === "ready"} fallback={
          <Show when={!busy()} fallback={<button class="btn" disabled>{phase() === "installing" ? "Installing…" : "Restarting…"}</button>}>
            <button class="btn btn-secondary" onClick={close}>Close</button>
            <Show when={phase() === "slow"}><button class="btn" onClick={() => location.reload()}>Reload</button></Show>
          </Show>
        }>
          <button class="btn btn-secondary" onClick={close}>Not now</button>
          <button class="btn" onClick={run}>Install {props.s()?.latest}</button>
        </Show>
      }>
      <Show when={props.s()}>{(s) => (
        <div class="stack">
          <p class="update-versions"><span class="mono">{s().current}</span> <span aria-hidden="true">→</span> <strong class="mono">{s().latest}</strong></p>
          <Show when={phase() === "ready"}>
            <p class="hint">The dashboard restarts for a few seconds. Running sites aren't touched.</p>
            <Show when={s().notes}><div class="update-notes">{s().notes.replace(/^#+\s*/gm, "").trim()}</div></Show>
            <a class="link" href={s().url} target="_blank" rel="noopener">Release on GitHub ↗</a>
          </Show>
          <Show when={phase() === "installing"}><p class="note">Downloading, checking its signature and installing…</p></Show>
          <Show when={phase() === "restarting"}><p class="note">Installed. Waiting for the dashboard to come back on {s().latest}; this page reloads by itself.</p></Show>
          <Show when={phase() === "slow"}><div class="note note-warn"><span>{s().latest} is installed, but the dashboard hasn't come back on it after 2 minutes. Reload to check; if the footer still shows the old version, nook's daily check restarts it, or run <span class="mono">sudo systemctl restart nook-web</span>.</span></div></Show>
          <Show when={phase() === "failed"}><pre class="output"><Ansi text={out()} /></pre></Show>
        </div>
      )}</Show>
    </Sheet>
  );
}

const ago = (iso?: string) => {
  if (!iso) return "never";
  const s = (Date.now() - Date.parse(iso)) / 1000;
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
};

// One update check shared by the banner and the footer.
export function useUpdate() {
  const [s, { mutate }] = createResource(() => api<UpdateStatus>("/api/update").catch(() => null));
  return { s, mutate };
}

export function UpdateBanner(props: { s: () => UpdateStatus | null | undefined }) {
  return (
    <Show when={props.s()?.available}>
      <div class="note update-banner">
        <span>nook <strong>{props.s()!.latest}</strong> is ready to install.</span>
        <span class="page-actions">
          <a class="btn btn-ghost btn-sm" href={props.s()!.url} target="_blank" rel="noopener">What's new</a>
          <button class="btn btn-sm" onClick={() => setUpdateOpen(true)}>Update</button>
        </span>
      </div>
    </Show>
  );
}

// The version this server runs, when it last looked for a release, and a way to look now.
export function UpdateFooter(props: { s: () => UpdateStatus | null | undefined; mutate: (v: UpdateStatus | null) => void }) {
  const [checking, setChecking] = createSignal(false);
  const check = async () => {
    setChecking(true);
    try {
      const r = await api<UpdateStatus>("/api/update/check", {});
      props.mutate(r);
      if (r.error) say("Couldn't check for updates", { kind: "error", detail: r.error });
      else if (r.available) setUpdateOpen(true); // found one: go straight to installing it
      else say("nook is up to date", { kind: "success" });
    } catch (e) { say("Couldn't check for updates", { kind: "error", detail: (e as Error).message }); }
    setChecking(false);
  };
  // the version serving this page, not just the one the update installed: if they differ, the restart didn't take
  const [running] = createResource(() => api<{ version: string }>("/api/version").then((r) => r.version).catch(() => ""));
  return (
    <footer class="update-footer">
      <UpdateSheet s={props.s} />
      <Show when={props.s()} fallback={<span>nook</span>}>
        {(s) => (<>
          <span>nook <span class="mono">{running() || s().current}</span></span>
          <Show when={running() && s().current && running() !== s().current && running() !== "dev"}>
            <span class="text-error" title="The update installed, but the dashboard's process hasn't restarted onto it. The next daily check restarts it.">{s().current} installed, not running yet</span>
          </Show>
          <Show when={!s().error} fallback={<span class="text-error" title={s().error}>update check failed</span>}>
            <span>{s().available ? `${s().latest} is ready` : s().checkedAt ? `up to date, checked ${ago(s().checkedAt)}` : "not checked yet"}</span>
          </Show>
          <Show when={s().available} fallback={<button class="btn btn-ghost btn-sm" disabled={checking()} onClick={check}>{checking() ? "Checking…" : "Check now"}</button>}>
            <button class="btn btn-sm" onClick={() => setUpdateOpen(true)}>Update</button>
          </Show>
        </>)}
      </Show>
    </footer>
  );
}
