import { A, useNavigate } from "@solidjs/router";
import { For, Show, createEffect, createMemo, createResource, createSignal, onCleanup, onMount, type JSX } from "solid-js";
import { Portal } from "solid-js/web";
import { Ansi } from "./ansi";
import { askLater, install, installPrompt, isAndroid, isIOS, markInstalled, shouldAskInstall } from "./install";
import { applyUpdate, updateReady } from "./update";
import { api, user, setUser, result, setResult, runCmd, notices, dismiss, say } from "./api";

type UpdateStatus = { current: string; latest: string; available: boolean; url: string; notes: string; checkedAt: string; error?: string };

const installUpdate = async (s: UpdateStatus) => {
  if (!window.confirm(`Install nook ${s.latest}? The dashboard restarts; running sites aren't affected.`)) return;
  await runCmd(`Updating to ${s.latest}`, "/api/update");
  setTimeout(() => location.reload(), 4000);
};

const ago = (iso?: string) => {
  if (!iso) return "never";
  const s = (Date.now() - Date.parse(iso)) / 1000;
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
};

// One update check shared by the banner and the footer.
function useUpdate() {
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
          <button class="btn btn-sm" onClick={() => installUpdate(props.s()!)}>Update</button>
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
      if (!r.error) say(r.available ? `nook ${r.latest} is ready to install` : "nook is up to date", { kind: r.available ? undefined : "success" });
    } catch (e) { say("Couldn't check for updates", { kind: "error", detail: (e as Error).message }); }
    setChecking(false);
  };
  return (
    <footer class="update-footer">
      <Show when={props.s()} fallback={<span>nook</span>}>
        {(s) => (<>
          <span>nook <span class="mono">{s().current}</span></span>
          <Show when={!s().error} fallback={<span class="text-error" title={s().error}>update check failed</span>}>
            <span>{s().available ? `${s().latest} is ready` : s().checkedAt ? `up to date, checked ${ago(s().checkedAt)}` : "not checked yet"}</span>
          </Show>
          <button class="btn btn-ghost btn-sm" disabled={checking()} onClick={check}>{checking() ? "Checking…" : "Check now"}</button>
        </>)}
      </Show>
    </footer>
  );
}

const links = [
  { href: "/", label: "Apps", end: true },
  { href: "/domains", label: "Domains" },
  { href: "/services", label: "Services" },
  { href: "/store", label: "Store" },
  { href: "/users", label: "Users" },
];

// On a wide screen the sections are in the top bar; on a phone they move to a dock under the thumb.
export function Nav() {
  const navigate = useNavigate();
  const logout = async () => { await api("/api/logout", {}); setUser(null); navigate("/login"); };
  return (
    <>
      <header class="navbar navbar-flush top-bar">
        <A href="/" class="brand"><Logo /></A>
        <nav class="menubar wide-only" aria-label="Sections">
          <For each={links}>{(l) => <A href={l.href} end={l.end} class="nav-link" activeClass="nav-active">{l.label}</A>}</For>
        </nav>
        <div class="navbar-end">
          <span class="text-secondary">{user()}</span>
          <button class="btn btn-ghost btn-sm" onClick={logout}>Log out</button>
        </div>
      </header>
      <Dock>
        <nav class="dock-nav" aria-label="Sections">
          <For each={links}>{(l) => <A href={l.href} end={l.end} class="dock-link" activeClass="nav-active">{l.label}</A>}</For>
        </nav>
      </Dock>
    </>
  );
}

// The dock publishes its height as --dock-h so notices and the page's bottom padding sit above it.
function Dock(props: { children: JSX.Element }) {
  let el!: HTMLDivElement;
  onMount(() => {
    const set = () => document.documentElement.style.setProperty("--dock-h", `${getComputedStyle(el).display === "none" ? 0 : el.offsetHeight}px`);
    const ro = new ResizeObserver(set);
    ro.observe(el);
    window.addEventListener("resize", set);
    set();
    onCleanup(() => { ro.disconnect(); window.removeEventListener("resize", set); document.documentElement.style.removeProperty("--dock-h"); });
  });
  return <div ref={el} class="dock phone-only">{props.children}</div>;
}

// Install the dashboard: build-docs' InstallAsk, low on the screen with the other notices.
function InstallAsk() {
  const [how, setHow] = createSignal(false);
  return (
    <Show when={user() && shouldAskInstall()}>
      <li class="notice install-ask">
        <img src="/icon-192.png" alt="" width="44" height="44" />
        <span class="notice-text">
          <strong>Install nook</strong>
          <small>Open your server from your home screen or dock, like any other app.</small>
          <Show when={how()}>
            <Show when={isIOS()} fallback={<small>Open your browser's menu, then tap <b>Install app</b> or <b>Add to Home screen</b>.</small>}>
              <small>In Safari, tap <b>Share</b>, then <b>Add to Home Screen</b>.</small>
            </Show>
          </Show>
          <span class="notice-actions">
            <Show when={installPrompt()} fallback={<Show when={(isIOS() || isAndroid()) && !how()}><button class="btn btn-sm" onClick={() => setHow(true)}>Show me how</button></Show>}>
              <button class="btn btn-sm" onClick={() => void install()}>Install</button>
            </Show>
            <Show when={how()}><button class="btn btn-sm btn-secondary" onClick={() => { markInstalled(); askLater(); }}>It's installed</button></Show>
            <button class="btn btn-sm btn-ghost" onClick={askLater}>Not now</button>
          </span>
        </span>
      </li>
    </Show>
  );
}

// A newer dashboard is on the server (nook updated itself while this page was open).
export function ReloadBanner() {
  return (
    <Show when={updateReady()}>
      <div class="note update-banner">
        <span>This page is from an older version of nook.</span>
        <span class="page-actions"><button class="btn btn-sm" onClick={() => void applyUpdate()}>Reload</button></span>
      </div>
    </Show>
  );
}

export function Notices() {
  return (
    <Portal>
      <ul class="notices" aria-live="polite">
        <InstallAsk />
        <For each={notices()}>
          {(n) => (
            <li class={`notice${n.kind ? ` notice-${n.kind}` : ""}`} onClick={() => dismiss(n.id)}>
              <span class="notice-text"><strong>{n.text}</strong><Show when={n.detail}><small>{n.detail}</small></Show></span>
              <Show when={n.action}>
                <span class="notice-actions"><button class="btn btn-secondary btn-sm" onClick={(e) => { e.stopPropagation(); dismiss(n.id); n.action!.run(); }}>{n.action!.label}</button></span>
              </Show>
            </li>
          )}
        </For>
      </ul>
    </Portal>
  );
}

// A sheet slides up from the bottom (centred from 700px). It closes with ×, Escape, Back, or a tap on the
// backdrop after the first 400 ms. Swipe-to-close is @sparkstone/kit's, which needs Solid 2.
let sheetIds = 0;

// The mark: an arch with an app inside. The window is a hole, so it sits on any background.
export function Logo() {
  return (
    <span class="brand-lockup">
      <svg class="brand-mark" viewBox="0 0 64 64" aria-hidden="true"><path d="M12 60V30a20 20 0 0 1 40 0v30Z M19 32a5 5 0 0 1 5-5h16a5 5 0 0 1 5 5v16a5 5 0 0 1-5 5H24a5 5 0 0 1-5-5Z" fill="currentColor" fill-rule="evenodd" /><rect x="25.5" y="33.5" width="13" height="13" rx="3" class="brand-tile" /></svg>
      nook
    </span>
  );
}

export function Sheet(props: { open: boolean; title: string; onClose: () => void; wide?: boolean; footer?: JSX.Element; children: JSX.Element }) {
  let opened = 0;
  let pushed = false;
  // Only open/closed matters: `open={!!props.app}` would otherwise rerun this (and step Back, closing the
  // sheet) every time the page refreshes its data and hands over a new object.
  const open = createMemo(() => !!props.open);
  createEffect(() => {
    if (!open()) return;
    opened = Date.now();
    const id = ++sheetIds; // this sheet's own history entry, so another sheet's Back step never closes it
    history.pushState({ ...history.state, sheet: id }, "");
    pushed = true;
    const onPop = () => { pushed = false; props.onClose(); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") close(); };
    window.addEventListener("popstate", onPop);
    window.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    onCleanup(() => {
      window.removeEventListener("popstate", onPop);
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
      // Step back over our own history entry, unless something navigated past it: a link in the sheet
      // unmounts the page before the router pushes the new URL, so look once that's happened.
      if (pushed) setTimeout(() => { if (history.state?.sheet === id) history.back(); });
      pushed = false;
    });
  });
  const close = () => props.onClose();
  return (
    <Show when={props.open}>
      <Portal>
        <div class="sheet-backdrop" onClick={(e) => { if (e.target === e.currentTarget && Date.now() - opened > 400) close(); }}>
          <div class="sheet" classList={{ "sheet-wide": props.wide }} role="dialog" aria-modal="true" aria-label={props.title}>
            <div class="sheet-grip" />
            <div class="sheet-header">
              <h2>{props.title}</h2>
              <button class="icon-btn" aria-label="Close" onClick={close}>×</button>
            </div>
            <div class="sheet-body">{props.children}</div>
            <Show when={props.footer}><div class="sheet-footer">{props.footer}</div></Show>
          </div>
        </div>
      </Portal>
    </Show>
  );
}

export function ResultSheet() {
  return (
    <Sheet open={!!result()} title={result()?.title ?? ""} onClose={() => setResult(null)} wide
      footer={<>
        <button class="btn btn-secondary" onClick={() => { navigator.clipboard?.writeText(result()?.out ?? ""); say("Output copied"); }}>Copy output</button>
        <button class="btn" onClick={() => setResult(null)}>Close</button>
      </>}>
      <Show when={result()}>
        {(r) => (
          <div class="stack">
            <Show when={r().warning}><div class="note note-warn">{r().warning}</div></Show>
            <Show when={r().cmd}>
              <p class="cmd-line"><code>$ {r().cmd}</code> <StatusBadge ok={r().ok}>exit {r().code}</StatusBadge></p>
            </Show>
            <pre class="output"><Ansi text={r().out || "(no output)"} /></pre>
          </div>
        )}
      </Show>
    </Sheet>
  );
}

// A status: ok is green, a problem is red, warn is amber, and none of them is plain.
export function StatusBadge(props: { ok?: boolean; warn?: boolean; children: JSX.Element; title?: string }) {
  const cls = () => (props.warn ? "badge-warning" : props.ok === true ? "badge-success" : props.ok === false ? "badge-error" : "");
  return <span class={`badge ${cls()}`} title={props.title}>{props.children}</span>;
}

export function Field(props: { label: string; children: JSX.Element; hint?: string; class?: string }) {
  return (
    <label class={`label ${props.class ?? ""}`}>
      {props.label}
      {props.children}
      <Show when={props.hint}><small class="hint">{props.hint}</small></Show>
    </label>
  );
}

export function ErrorAlert(props: { error?: string }) {
  return <Show when={props.error}><div class="note note-bad" role="alert">{props.error}</div></Show>;
}

// The head of a page: an optional way back, the title, a muted detail, and the page's actions at the end.
export function PageHead(props: { title: JSX.Element; back?: { href: string; label: string }; detail?: JSX.Element; children?: JSX.Element }) {
  return (
    <div class="page-head">
      <Show when={props.back}><div class="back-row"><A class="btn btn-ghost btn-sm back" href={props.back!.href}>‹ {props.back!.label}</A></div></Show>
      <h1>{props.title}</h1>
      <Show when={props.detail}><span class="text-secondary">{props.detail}</span></Show>
      <Show when={props.children}><div class="page-actions">{props.children}</div></Show>
    </div>
  );
}

export { useUpdate };

// A deploy token, shown once: the token with its own Copy, and the gh command that stores it in the repo,
// apart from the setup instructions (which never contain it).
export function TokenBox(props: { token: string; repo?: string }) {
  const repo = () => props.repo || "OWNER/REPO";
  const gh = () => `gh secret set NOOK_TOKEN --repo ${repo()} --body '${props.token}'`;
  const copy = (text: string, what: string) => { navigator.clipboard?.writeText(text); say(`${what} copied`); };
  return (
    <div class="token-box stack">
      <div class="note note-warn"><span>Shown once: the server keeps only its hash. Store it as the repo's <span class="mono">NOOK_TOKEN</span> secret now.</span></div>
      <Field label="Deploy token">
        <div class="field-row">
          <input class="input mono" readonly value={props.token} onFocus={(e) => e.currentTarget.setSelectionRange(0, e.currentTarget.value.length)} />
          <button type="button" class="btn btn-secondary" onClick={() => copy(props.token, "Token")}>Copy</button>
        </div>
      </Field>
      <Field label="Or store it with gh" hint={props.repo ? undefined : "Put your repo in place of OWNER/REPO, or set the repo on the Deploy page."}>
        <div class="field-row">
          <input class="input mono" readonly value={gh()} onFocus={(e) => e.currentTarget.setSelectionRange(0, e.currentTarget.value.length)} />
          <button type="button" class="btn btn-secondary" onClick={() => copy(gh(), "Command")}>Copy</button>
        </div>
      </Field>
    </div>
  );
}
