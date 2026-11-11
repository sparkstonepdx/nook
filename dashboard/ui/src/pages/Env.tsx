import { For, Show, createResource, createSignal } from "solid-js";
import { A, useParams, useSearchParams } from "@solidjs/router";
import { api, runCmd } from "../api";
import { Field } from "../components";

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
    <div class="stack">
      <div class="page-head">
        <A class="btn btn-ghost btn-sm" href="/">← Apps</A>
        <h1>{params.app}: environment</h1>
      </div>
      <section class="card">
        <Field label="Scope">
          <select class="select" value={scope()} onChange={(e) => setSearch({ scope: e.currentTarget.value === "app" ? undefined : e.currentTarget.value })}>
            <For each={scopes()}>{(s) => <option value={s}>{label(s)}</option>}</For>
          </select>
        </Field>
        <p class="text-secondary hint">
          {explain[kind()]} Saving restarts the running sites that load it
          <Show when={data()?.affected.length} fallback=" (none are running right now)"> ({data()!.affected.join(", ")})</Show>.
          Load order, later wins: the app's own secrets.env, sent by CI, whole app, services, previews, sent by deploys, site.
          nook sets PORT, HOST, SITE and SITE_HOST itself.
        </p>
      </section>
      <Show when={data.error}><div class="alert alert-error">{String(data.error.message)}</div></Show>

      <section class="card">
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>Name</th><th>Value</th><th /></tr></thead>
            <tbody>
              <For each={data()?.vars} fallback={<tr><td colspan={3} class="text-secondary">No variables yet.</td></tr>}>
                {(v) => (
                  <tr>
                    <td class="mono">{v.key}</td>
                    <td class="mono env-value">{shown().has(v.key) ? v.value : "••••••••"}</td>
                    <td><div class="actions">
                      <button class="btn btn-ghost btn-sm" onClick={() => toggle(v.key)}>{shown().has(v.key) ? "Hide" : "Show"}</button>
                      <Show when={!["deploy", "ci"].includes(kind())}><button class="btn btn-ghost btn-sm" onClick={() => window.confirm(`Remove ${v.key}? Running sites restart.`) && apply(`Removed ${v.key}`, { unset: [v.key] })}>Remove</button></Show>
                    </div></td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </section>

      <Show when={!["deploy", "ci"].includes(kind())}>
      <section class="card">
        <h2>Set a variable</h2>
        <p class="text-secondary hint">Setting an existing name replaces its value.</p>
        <form class="form-row" style={{ "margin-top": "0.75rem" }} onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const f = new FormData(form);
          const key = String(f.get("key")), value = String(f.get("value"));
          apply(`Set ${key}`, { set: { [key]: value } }).then(() => form.reset());
        }}>
          <Field label="Name"><input class="input mono" name="key" required pattern="[A-Za-z_][A-Za-z0-9_]*" placeholder="VAPID_PRIVATE_KEY" autocomplete="off" /></Field>
          <Field label="Value"><input class="input mono" name="value" type="password" autocomplete="off" placeholder="value" /></Field>
          <button class="btn btn-primary">Save</button>
        </form>
      </section>

      <section class="card">
        <h2>Paste several</h2>
        <p class="text-secondary hint">KEY=value lines, like a .env file. Blank lines and # comments are ignored.</p>
        <form class="stack" style={{ "margin-top": "0.75rem" }} onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const set: Record<string, string> = {};
          for (const line of String(new FormData(form).get("lines")).split("\n")) {
            const m = line.match(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$/);
            if (m) set[m[1]] = m[2].trim().replace(/^"(.*)"$/, "$1").replace(/^'(.*)'$/, "$1");
          }
          if (Object.keys(set).length) apply(`Set ${Object.keys(set).join(", ")}`, { set }).then(() => form.reset());
        }}>
          <textarea class="textarea mono" name="lines" rows={5} placeholder={"DATABASE_URL=...\nAPI_KEY=..."} autocomplete="off" spellcheck={false} />
          <div><button class="btn btn-secondary">Save all</button></div>
        </form>
      </section>
      </Show>
    </div>
  );
}
