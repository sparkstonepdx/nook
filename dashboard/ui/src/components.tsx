import { A, useNavigate } from "@solidjs/router";
import { Show, createEffect, type JSX } from "solid-js";
import { api, user, setUser, result, setResult } from "./api";

export function Nav() {
  const navigate = useNavigate();
  const logout = async () => { await api("/api/logout", {}); setUser(null); navigate("/login"); };
  return (
    <nav class="navbar">
      <div class="navbar-start">
        <A href="/" class="semantic brand">app-host</A>
        <div class="menubar">
          <A href="/" end class="btn btn-ghost" activeClass="nav-active">Apps</A>
          <A href="/domains" class="btn btn-ghost" activeClass="nav-active">Domains</A>
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
