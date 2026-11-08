import { For, Show, createResource, createSignal } from "solid-js";
import { A, useParams } from "@solidjs/router";
import { api, runCmd } from "../api";
import { Field } from "../components";

type Env = { vars: { key: string; value: string }[]; running: string[] };

export default function EnvPage() {
  const params = useParams();
  const [data, { refetch }] = createResource(() => params.app, (app) => api<Env>(`/api/apps/${app}/env`));
  const [shown, setShown] = createSignal<Set<string>>(new Set());
  const toggle = (k: string) => { const s = new Set(shown()); s.has(k) ? s.delete(k) : s.add(k); setShown(s); };
  const apply = async (title: string, body: { set?: Record<string, string>; unset?: string[] }) => { await runCmd(title, `/api/apps/${params.app}/env`, body); refetch(); };

  return (
    <div class="stack">
      <div class="page-head">
        <A class="btn btn-ghost btn-sm" href="/">← Apps</A>
        <h1>{params.app}: environment</h1>
      </div>
      <p class="text-secondary hint">
        Loaded into every running site of this app. Saving restarts them
        <Show when={data()?.running.length}> ({data()!.running.join(", ")})</Show>.
        app-host sets PORT, HOST, SITE and SITE_HOST itself. Values the app's own deploys write to secrets.env also load; these override them.
      </p>
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
                      <button class="btn btn-ghost btn-sm" onClick={() => window.confirm(`Remove ${v.key}? Running sites restart.`) && apply(`Removed ${v.key}`, { unset: [v.key] })}>Remove</button>
                    </div></td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </section>

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
    </div>
  );
}
