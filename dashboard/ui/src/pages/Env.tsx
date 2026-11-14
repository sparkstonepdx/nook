import { For, Show, createResource, createSignal } from "solid-js";
import { A, useParams, useSearchParams } from "@solidjs/router";
import { api, runCmd } from "../api";
import { Field, PageHead, Confirm } from "../components";

type Env = { vars: { key: string; value: string }[]; running: string[]; affected: string[]; scope: string; scopes: string[]; sites: string[] };

const isPreview = (s: string) => /^pr-\d+\./.test(s) || /-pr-\d+$/.test(s);
const label = (scope: string) => {
  if (scope === "app") return "Whole app";
  if (scope === "ci") return "Sent by CI (whole app)";
  const [kind, name] = scope.split(":");
  return { site: `Site: ${name}`, previews: `Previews of ${name}`, deploy: `Sent by deploys: ${name}` }[kind] ?? scope;
};
const explain: Record<string, string> = {
  app: "Loaded into every running site of this app.",
  site: "Loaded into this site only, after the app's values, so these win.",
  previews: "Loaded into every PR preview of this site (not the site itself). Previews never get the site's own values, so production secrets stay out of PR builds unless you put them here or in the whole app.",
  deploy: "Sent with the latest deploy of this site (or by the env API for this site). Set a value in the site's own scope to override it.",
  ci: "Sent by CI through the env API, for every site of this app. Your whole-app values load after these, so they win.",
};

export default function EnvPage() {
  const params = useParams();
  const [search, setSearch] = useSearchParams();
  const scope = () => (search.scope as string) || "app";
  const kind = () => scope().split(":")[0];
  const [data, { refetch }] = createResource(() => [params.app, scope()] as const, ([app, sc]) => api<Env>(`/api/apps/${app}/env?scope=${encodeURIComponent(sc)}`));
  const scopes = () => {
    const d = data();
    const all = ["app"];
    for (const s of d?.sites ?? []) if (!isPreview(s)) all.push(`site:${s}`, `previews:${s}`);
    for (const s of d?.scopes ?? []) if (!all.includes(s)) all.push(s);
    if (!all.includes(scope())) all.push(scope());
    return all;
  };
  const [shown, setShown] = createSignal<Set<string>>(new Set());
  const toggle = (k: string) => { const s = new Set(shown()); s.has(k) ? s.delete(k) : s.add(k); setShown(s); };
  const apply = async (title: string, body: { set?: Record<string, string>; unset?: string[] }) => { await runCmd(title, `/api/apps/${params.app}/env`, { ...body, scope: scope() }); refetch(); };

  return (
    <>
      <PageHead title="Environment" back={{ href: "/", label: "Apps" }} detail={params.app} />
      <section class="section">
        <div class="card stack">
          <Field label="Scope">
            <select class="select" onChange={(e) => setSearch({ scope: e.currentTarget.value === "app" ? undefined : e.currentTarget.value })}>
              <For each={scopes()}>{(s) => <option value={s} selected={s === scope()}>{label(s)}</option>}</For>
            </select>
          </Field>
          <p class="hint">
            {explain[kind()]} Saving restarts the running sites that load it
            <Show when={data()?.affected.length} fallback=" (none are running right now)"> ({data()!.affected.join(", ")})</Show>.
          </p>
          <details class="hint">
            <summary>Load order</summary>
            Later wins: the app's own secrets.env, sent by CI, whole app, services, previews, sent by deploys, site. nook sets PORT, HOST, SITE and SITE_HOST itself.
          </details>
        </div>
      </section>
      <Show when={data.error}><div class="note note-bad">{String(data.error!.message)}</div></Show>

      <section class="section">
        <h2>Variables</h2>
        <ul class="list">
          <For each={data()?.vars} fallback={<li class="row"><span class="empty">No variables yet.</span></li>}>
            {(v) => (
              <li class="row">
                <span class="row-main"><span class="mono">{v.key}</span><small class="mono env-value">{shown().has(v.key) ? v.value : "••••••••"}</small></span>
                <span class="row-end">
                  <button class="btn btn-ghost btn-sm" onClick={() => toggle(v.key)}>{shown().has(v.key) ? "Hide" : "Show"}</button>
                  <Show when={!["deploy", "ci"].includes(kind())}><Confirm label="Remove" yes="Remove" ask={`Remove ${v.key}? Running sites restart.`} onYes={() => apply(`Removed ${v.key}`, { unset: [v.key] })} /></Show>
                </span>
              </li>
            )}
          </For>
        </ul>
      </section>

      <Show when={!["deploy", "ci"].includes(kind())}>
      <section class="section">
        <h2>Set a variable</h2>
        <form class="card stack" onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const f = new FormData(form);
          const key = String(f.get("key")), value = String(f.get("value"));
          apply(`Set ${key}`, { set: { [key]: value } }).then(() => form.reset());
        }}>
          <div class="field-row">
            <Field label="Name"><input class="input mono" name="key" required pattern="[A-Za-z_][A-Za-z0-9_]*" placeholder="VAPID_PRIVATE_KEY" autocomplete="off" /></Field>
            <Field label="Value"><input class="input mono" name="value" type="password" autocomplete="off" placeholder="value" /></Field>
            <button class="btn">Save</button>
          </div>
          <p class="hint">Setting a name that exists replaces its value.</p>
        </form>
      </section>

      <section class="section">
        <h2>Paste several</h2>
        <form class="card stack" onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const set: Record<string, string> = {};
          for (const line of String(new FormData(form).get("lines")).split("\n")) {
            const m = line.match(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$/);
            if (m) set[m[1]] = m[2].trim().replace(/^"(.*)"$/, "$1").replace(/^'(.*)'$/, "$1");
          }
          if (Object.keys(set).length) apply(`Set ${Object.keys(set).join(", ")}`, { set }).then(() => form.reset());
        }}>
          <Field label="Lines" hint="KEY=value, like a .env file. Blank lines and # comments are skipped.">
            <textarea class="textarea mono" name="lines" rows={5} placeholder={"DATABASE_URL=...\nAPI_KEY=..."} autocomplete="off" spellcheck={false} />
          </Field>
          <button class="btn btn-secondary" style={{ "align-self": "flex-start" }}>Save all</button>
        </form>
      </section>
      </Show>
    </>
  );
}
