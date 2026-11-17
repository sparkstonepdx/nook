import { For, Show, createResource, createSignal } from "solid-js";
import { api, runCmd } from "./api";
import { Confirm, StatusBadge } from "./components";
import { ago } from "./format";

type Pkg = { name: string; from: string; to: string; security: boolean; app?: string };
type SystemStatus = {
  os: string; kernel: string; packages: Pkg[]; security: number; restart: boolean; restartWhy?: string; auto: boolean;
  checkedAt?: string; updatedAt?: string; error?: string; upSince?: string; held?: string[]; stale: string[];
};

const unix = (iso?: string) => (iso ? Date.parse(iso) / 1000 : undefined);
const unitLabel = (u: string) => u.replace(/^nook-site-/, "").replace(/\.service$/, "").replace("--", " / ");

// The server's own packages: what's waiting, security fixes first, updating them, and restarting when a new
// kernel needs it. Store apps' packages (Postgres) aren't updated here: they follow their app's update policy.
export function SystemUpdates() {
  const [s, { refetch }] = createResource(() => api<SystemStatus>("/api/system"));
  const [busy, setBusy] = createSignal("");
  const act = async (what: string, title: string, path: string, body: unknown = {}) => {
    setBusy(what);
    await runCmd(title, path, body);
    setBusy("");
    refetch();
  };
  const own = () => (s()?.packages ?? []).filter((p) => !p.app);
  const withApps = () => (s()?.packages ?? []).filter((p) => p.app);
  const summary = () => {
    const n = own().length, sec = s()!.security;
    if (!n) return "Up to date";
    return `${n} ${n === 1 ? "update" : "updates"} waiting${sec ? `, ${sec} of them security ${sec === 1 ? "fix" : "fixes"}` : ""}`;
  };
  return (
    <Show when={s()}>
      {(st) => (
        <div class="stack system-updates">
          <ul class="list">
            <li class="row">
              <span class={`status-dot status-${st().security ? "tight" : own().length ? "watch" : "ok"}`} aria-hidden="true" />
              <span class="row-main">
                <span>{summary()}</span>
                <small>
                  {st().os || "System"} · kernel <span class="mono">{st().kernel}</span>
                  {st().checkedAt ? ` · checked ${ago(unix(st().checkedAt))}` : " · not checked yet"}
                  {st().updatedAt ? ` · updated ${ago(unix(st().updatedAt))}` : ""}
                </small>
                <Show when={st().error}><small class="text-error">{st().error}</small></Show>
              </span>
              <span class="row-end">
                <button class="btn btn-secondary btn-sm" disabled={!!busy()} onClick={() => act("check", "Checked for system updates", "/api/system/check")}>{busy() === "check" ? "Checking…" : "Check now"}</button>
                <Show when={own().length}>
                  <Show when={st().security && st().security < own().length}>
                    <button class="btn btn-secondary btn-sm" disabled={!!busy()} onClick={() => act("security", "Installed the security fixes", "/api/system/update", { security: true })}>{busy() === "security" ? "Installing…" : "Security only"}</button>
                  </Show>
                  <button class="btn btn-sm" disabled={!!busy()} onClick={() => act("all", "Updated the system", "/api/system/update", { security: false })}>{busy() === "all" ? "Updating…" : "Update"}</button>
                </Show>
              </span>
            </li>
            <Show when={st().restart}>
              <li class="row">
                <span class="status-dot status-watch" aria-hidden="true" />
                <span class="row-main"><span>Restart needed</span><small>{st().restartWhy}. Sites are down for the minute or so it takes.</small></span>
                <span class="row-end">
                  <Confirm label="Restart server" ask="Restart the server? Every site goes down until it's back." yes="Restart"
                    onYes={() => act("restart", "Restarting the server; this page comes back when it's up", "/api/system/restart", { confirm: "restart" })} />
                </span>
              </li>
            </Show>
            <Show when={st().stale.length}>
              <li class="row">
                <span class="status-dot status-watch" aria-hidden="true" />
                <span class="row-main"><span>Still on replaced libraries</span><small>{st().stale.map(unitLabel).join(", ")} {st().stale.length === 1 ? "keeps" : "keep"} running the old code until restarted; the next update restarts {st().stale.length === 1 ? "it" : "them"}.</small></span>
              </li>
            </Show>
          </ul>
          <Show when={st().packages.length}>
            <details class="system-packages">
              <summary>The packages</summary>
              <ul class="list">
                <For each={own()}>
                  {(p) => (
                    <li class="row"><span class="row-main"><span class="mono">{p.name}</span><small class="mono">{p.from} → {p.to}</small></span>
                      <Show when={p.security}><span class="row-end"><StatusBadge ok={false}>security</StatusBadge></span></Show></li>
                  )}
                </For>
                <For each={withApps()}>
                  {(p) => <li class="row"><span class="row-main"><span class="mono">{p.name}</span><small>Updated with {p.app}, by its update policy in the Store</small></span></li>}
                </For>
              </ul>
            </details>
          </Show>
          <label class="switch-row">
            <input type="checkbox" role="switch" checked={st().auto} onChange={(e) => act("auto", e.currentTarget.checked ? "Security fixes install by themselves" : "Security fixes wait for you", "/api/system/auto", { on: e.currentTarget.checked })} />
            <span><span>Install security fixes by themselves</span><small>Once a day, with nook's own update check. Other updates and restarts still wait for you.</small></span>
          </label>
        </div>
      )}
    </Show>
  );
}
