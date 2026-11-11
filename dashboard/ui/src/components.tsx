import { A, useNavigate } from "@solidjs/router";
import { Show, createEffect, createResource, createSignal, type JSX } from "solid-js";
import { api, user, setUser, result, setResult, runCmd } from "./api";

type UpdateStatus = { current: string; latest: string; available: boolean; url: string; notes: string; checkedAt: string; error?: string };

export function UpdateBanner() {
  const [s] = createResource(() => api<UpdateStatus>("/api/update").catch(() => null));
  const install = async () => {
    if (!window.confirm(`Install nook ${s()!.latest}? The dashboard restarts; running sites aren't affected.`)) return;
    await runCmd(`Updating to ${s()!.latest}`, "/api/update");
    setTimeout(() => location.reload(), 4000);
  };
  return (
    <Show when={s()?.available}>
      <div class="alert update-banner">
        <span>nook <strong>{s()!.latest}</strong> is available (running {s()!.current}).</span>
        <a class="link" href={s()!.url} target="_blank" rel="noopener">What's new</a>
        <span class="spacer" />
        <button class="btn btn-primary btn-sm" onClick={install}>Update</button>
      </div>
    </Show>
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

// UpdateFooter is on every page: the version this server runs, when it last looked for a release, and
// a way to look now and install.
export function UpdateFooter() {
  const [s, { mutate }] = createResource(() => api<UpdateStatus>("/api/update").catch(() => null));
  const [checking, setChecking] = createSignal(false);
  const check = async () => {
    setChecking(true);
    try { mutate(await api<UpdateStatus>("/api/update/check", {})); } catch { /* shown via status */ }
    setChecking(false);
  };
  const install = async () => {
    if (!window.confirm(`Install nook ${s()!.latest}? The dashboard restarts; running sites aren't affected.`)) return;
    await runCmd(`Updating to ${s()!.latest}`, "/api/update");
    setTimeout(() => location.reload(), 4000);
  };
  return (
    <footer class="update-footer text-secondary">
      <Show when={s()} fallback={<span>nook</span>}>
        <span>nook <span class="mono">{s()!.current}</span></span>
        <span>·</span>
        <Show when={!s()!.error} fallback={<span class="text-error" title={s()!.error}>update check failed: {s()!.error}</span>}>
          <Show when={s()!.available} fallback={<span>{s()!.checkedAt ? `up to date (checked ${ago(s()!.checkedAt)})` : "not checked for updates yet"}</span>}>
            <span><strong>{s()!.latest}</strong> is available</span>
          </Show>
        </Show>
        <button class="btn btn-ghost btn-sm" disabled={checking()} onClick={check}>{checking() ? "Checking…" : "Check now"}</button>
        <Show when={s()!.available}>
          <a class="link" href={s()!.url} target="_blank" rel="noopener">What's new</a>
          <button class="btn btn-primary btn-sm" onClick={install}>Update</button>
        </Show>
      </Show>
    </footer>
  );
}

export function Nav() {
  const navigate = useNavigate();
  const logout = async () => { await api("/api/logout", {}); setUser(null); navigate("/login"); };
  return (
    <nav class="navbar">
      <div class="navbar-start">
        <A href="/" class="semantic brand">nook</A>
        <div class="menubar">
          <A href="/" end class="btn btn-ghost" activeClass="nav-active">Apps</A>
          <A href="/domains" class="btn btn-ghost" activeClass="nav-active">Domains</A>
          <A href="/services" class="btn btn-ghost" activeClass="nav-active">Services</A>
          <A href="/store" class="btn btn-ghost" activeClass="nav-active">Store</A>
          <A href="/users" class="btn btn-ghost" activeClass="nav-active">Users</A>
        </div>
      </div>
      <div class="navbar-end">
        <span class="text-secondary">{user()}</span>
        <button class="btn btn-secondary" onClick={logout}>Log out</button>
      </div>
    </nav>
  );
}

export function ResultDialog() {
  let dialog!: HTMLDialogElement;
  createEffect(() => { if (result()) dialog.showModal(); else dialog.close(); });
  return (
    <dialog ref={dialog} class="dialog" onClose={() => setResult(null)}>
      <Show when={result()}>
        {(r) => (
          <div class="dialog-box result">
            <h3 class="dialog-header">{r().title}</h3>
            <Show when={r().warning}><div class="alert alert-error">{r().warning}</div></Show>
            <Show when={r().cmd}>
              <p class="text-secondary">
                <code>$ {r().cmd}</code> <StatusBadge ok={r().ok}>exit {r().code}</StatusBadge>
              </p>
            </Show>
            <pre class="output">{r().out || "(no output)"}</pre>
            <div class="dialog-actions">
              <button class="btn btn-secondary" onClick={() => navigator.clipboard?.writeText(r().out)}>Copy output</button>
              <button class="btn" onClick={() => setResult(null)}>Close</button>
            </div>
          </div>
        )}
      </Show>
    </dialog>
  );
}

export function StatusBadge(props: { ok?: boolean; warn?: boolean; children: JSX.Element; title?: string }) {
  return <span class="badge" classList={{ "status-ok": props.ok === true, "status-bad": props.ok === false || props.warn }} title={props.title}>{props.children}</span>;
}

export function Field(props: { label: string; children: JSX.Element; hint?: string }) {
  return (
    <label class="field">
      <span class="label">{props.label}</span>
      {props.children}
      <Show when={props.hint}><small class="text-secondary">{props.hint}</small></Show>
    </label>
  );
}

export function ErrorAlert(props: { error?: string }) {
  return <Show when={props.error}><div class="alert alert-error" role="alert">{props.error}</div></Show>;
}
