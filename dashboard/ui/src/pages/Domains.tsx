import { For, Show, createResource, createSignal } from "solid-js";
import { api, runCmd } from "../api";
import { Field, StatusBadge } from "../components";
import { grantLabel } from "./Apps";

type Domains = {
  baseDomain: string; ip: string; apps: string[];
  grants: { app: string; kind: string; name: string }[];
  dns: { type: string; name: string; value: string; note: string }[];
  dashboardHost: string; ipAccess: boolean; ipPort: string;
};

export default function DomainsPage() {
  const [data, { refetch }] = createResource(() => api<Domains>("/api/domains"));
  const after = async (p?: Promise<unknown>) => { await p; refetch(); };
  const [kind, setKind] = createSignal("domain");

  return (
    <div class="stack">
      <div class="page-head"><h1>Domains</h1></div>
      <Show when={data.error}><div class="alert alert-error">{String(data.error.message)}</div></Show>
      <Show when={data()}>{(d) => (<>
        <section class="card">
          <h2>What each app may use</h2>
          <div class="table-wrap">
            <table class="table">
              <thead><tr><th>App</th><th>Allowed</th><th /></tr></thead>
              <tbody>
                <For each={d().grants} fallback={<tr><td colspan={3} class="text-secondary">Nothing granted yet.</td></tr>}>
                  {(g) => (
                    <tr>
                      <td>{g.app}</td>
                      <td class="mono">{grantLabel(g.kind, g.name)}</td>
                      <td><div class="actions">
                        <button class="btn btn-ghost btn-sm" onClick={() => window.confirm(`Remove ${grantLabel(g.kind, g.name)} from ${g.app}? Its sites on it go offline.`) && after(runCmd(`Removed ${grantLabel(g.kind, g.name)} from ${g.app}`, "/api/grants", { ...g, op: "remove" }))}>Remove</button>
                      </div></td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
          <form class="form-row" style={{ "margin-top": "1rem" }} onSubmit={(e) => {
            e.preventDefault();
            const f = Object.fromEntries(new FormData(e.currentTarget));
            after(runCmd(`Allowed ${grantLabel(String(f.kind), String(f.name))} for ${f.app}`, "/api/grants", { ...f, op: "add" }));
          }}>
            <Field label="App"><select class="select" name="app" required><For each={d().apps}>{(a) => <option>{a}</option>}</For></select></Field>
            <Field label="May use">
              <select class="select" name="kind" value={kind()} onChange={(e) => setKind(e.currentTarget.value)}>
                <option value="domain">Whole domain (root + subdomains)</option>
                <option value="sub">Subdomains only</option>
                <option value="host">Exactly this hostname</option>
              </select>
            </Field>
            <Field label={kind() === "host" ? "Hostname" : "Domain"}><input class="input" name="name" required placeholder={kind() === "host" ? "blog.client.com" : "client.com"} /></Field>
            <button class="btn btn-primary">Allow</button>
          </form>
          <p class="text-secondary hint">The first app to publish a hostname keeps it. A root domain with no www folder gets www redirecting to it.</p>
        </section>

        <section class="card">
          <h2>DNS records needed</h2>
          <div class="table-wrap">
            <table class="table mono">
              <thead><tr><th>Type</th><th>Name</th><th>Value</th><th /></tr></thead>
              <tbody><For each={d().dns}>{(r) => <tr><td>{r.type}</td><td>{r.name}</td><td>{r.value}</td><td class="text-secondary">{r.note}</td></tr>}</For></tbody>
            </table>
          </div>
        </section>

        <section class="card">
          <h2>Dashboard address</h2>
          <p class="text-secondary hint">Point DNS for it at {d().ip} first. No app can use this hostname. Leave empty to use only the IP address.</p>
          <form class="form-row" style={{ "margin-top": "0.75rem" }} onSubmit={(e) => {
            e.preventDefault();
            const host = String(new FormData(e.currentTarget).get("host") || "");
            after(runCmd(host ? `Dashboard address is now ${host}` : "Dashboard address removed", "/api/dashboard-host", { host }));
          }}>
            <Field label="Hostname"><input class="input" name="host" value={d().dashboardHost} placeholder={`admin.${d().baseDomain}`} /></Field>
            <button class="btn btn-primary">Save</button>
          </form>
          <div class="row" style={{ "margin-top": "1rem" }}>
            <span>Backup: <span class="mono">https://{d().ip}:{d().ipPort}</span> (self-signed)</span>
            <StatusBadge ok={d().ipAccess}>{d().ipAccess ? "on" : "off"}</StatusBadge>
            <span class="spacer" />
            <Show when={d().ipAccess} fallback={<button class="btn btn-secondary" onClick={() => after(runCmd("IP access on", "/api/ip-access", { on: true }))}>Turn on</button>}>
              <button class="btn btn-ghost" disabled={!d().dashboardHost} title={d().dashboardHost ? "" : "Set a dashboard address first"}
                onClick={() => window.confirm("Turn off IP access? If DNS for the dashboard address breaks, you'll need SSH to get back in.") && after(runCmd("IP access off", "/api/ip-access", { on: false }))}>Turn off</button>
            </Show>
          </div>
          <p class="text-secondary hint">Keep it on so you can still get in if DNS breaks.</p>
        </section>

        <section class="card">
          <h2>Default domain</h2>
          <p class="text-secondary hint">Site folders without a dot ("blog") are subdomains of this. Changing it moves every app's *.{d().baseDomain} grant to the new domain.</p>
          <form class="form-row" style={{ "margin-top": "0.75rem" }} onSubmit={(e) => {
            e.preventDefault();
            const domain = String(new FormData(e.currentTarget).get("domain") || "");
            if (domain !== d().baseDomain && window.confirm(`Change the default domain to ${domain}? Every short site name moves with it.`))
              after(runCmd(`Default domain is now ${domain}`, "/api/default-domain", { domain }));
          }}>
            <Field label="Domain"><input class="input" name="domain" value={d().baseDomain} required /></Field>
            <button class="btn btn-secondary">Change</button>
          </form>
        </section>
      </>)}</Show>
    </div>
  );
}
