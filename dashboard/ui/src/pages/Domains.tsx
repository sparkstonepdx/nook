import { For, Show, createResource, createSignal } from "solid-js";
import { api, runCmd } from "../api";
import { Field, PageHead, Sheet, StatusBadge } from "../components";
import { grantLabel } from "./Apps";

type Domains = {
  baseDomain: string; ip: string; apps: string[];
  grants: { app: string; kind: string; name: string }[];
  dns: { type: string; name: string; value: string; note: string }[];
  dashboardHost: string; ipAccess: boolean; ipPort: string;
};

const kindLabel: Record<string, string> = { domain: "the domain and its subdomains", sub: "subdomains only", host: "this hostname" };

export default function DomainsPage() {
  const [data, { refetch }] = createResource(() => api<Domains>("/api/domains"));
  const after = async (p?: Promise<unknown>) => { await p; refetch(); };
  const [kind, setKind] = createSignal("domain");
  const [allowing, setAllowing] = createSignal(false);

  return (
    <>
      <PageHead title="Domains" detail={data()?.baseDomain}>
        <button class="btn" onClick={() => setAllowing(true)}>Allow a domain</button>
      </PageHead>
      <Show when={data.error}><div class="note note-bad">{String(data.error!.message)}</div></Show>
      <Show when={data()}>{(d) => (<>
        <section class="section">
          <h2>What each app may use</h2>
          <ul class="list">
            <For each={d().grants} fallback={<li class="row"><span class="empty">Nothing allowed yet.</span></li>}>
              {(g) => (
                <li class="row">
                  <span class="row-main"><span class="mono">{grantLabel(g.kind, g.name)}</span><small>{g.app} · {kindLabel[g.kind]}</small></span>
                  <span class="row-end">
                    <button class="btn btn-ghost btn-sm" onClick={() => window.confirm(`Remove ${grantLabel(g.kind, g.name)} from ${g.app}? Its sites on it go offline.`) && after(runCmd(`Removed ${grantLabel(g.kind, g.name)} from ${g.app}`, "/api/grants", { ...g, op: "remove" }))}>Remove</button>
                  </span>
                </li>
              )}
            </For>
          </ul>
          <p class="hint">The first app to publish a hostname keeps it. A root domain with no www folder gets www redirecting to it.</p>
        </section>

        <section class="section">
          <h2>DNS records</h2>
          <div class="card table-wrap flush">
            <table class="table">
              <thead><tr><th>Type</th><th>Name</th><th>Value</th><th>For</th></tr></thead>
              <tbody><For each={d().dns}>{(r) => <tr><td class="mono">{r.type}</td><td class="mono">{r.name}</td><td class="mono">{r.value}</td><td class="text-secondary">{r.note}</td></tr>}</For></tbody>
            </table>
          </div>
        </section>

        <section class="section">
          <h2>Dashboard address</h2>
          <div class="stack">
          <form class="card stack" onSubmit={(e) => {
            e.preventDefault();
            const host = String(new FormData(e.currentTarget).get("host") || "");
            after(runCmd(host ? `Dashboard address is now ${host}` : "Dashboard address removed", "/api/dashboard-host", { host }));
          }}>
            <div class="field-row">
              <Field label="Hostname" hint={`Point its DNS at ${d().ip} first. Leave it empty to use only the IP address.`}><input class="input" name="host" value={d().dashboardHost} placeholder={`admin.${d().baseDomain}`} /></Field>
              <button class="btn btn-secondary">Save</button>
            </div>
          </form>
          <ul class="list">
            <li class="row">
              <span class="row-main"><span>Backup address <StatusBadge ok={d().ipAccess}>{d().ipAccess ? "on" : "off"}</StatusBadge></span><small><span class="mono">https://{d().ip}:{d().ipPort}</span>, self-signed. Keep it on so you can still get in if DNS breaks.</small></span>
              <span class="row-end">
                <Show when={d().ipAccess} fallback={<button class="btn btn-secondary btn-sm" onClick={() => after(runCmd("IP access on", "/api/ip-access", { on: true }))}>Turn on</button>}>
                  <button class="btn btn-ghost btn-sm" disabled={!d().dashboardHost} title={d().dashboardHost ? "" : "Set a dashboard address first"}
                    onClick={() => window.confirm("Turn off IP access? If DNS for the dashboard address breaks, you'll need SSH to get back in.") && after(runCmd("IP access off", "/api/ip-access", { on: false }))}>Turn off</button>
                </Show>
              </span>
            </li>
          </ul>
          </div>
        </section>

        <section class="section">
          <h2>Default domain</h2>
          <form class="card stack" onSubmit={(e) => {
            e.preventDefault();
            const domain = String(new FormData(e.currentTarget).get("domain") || "");
            if (domain !== d().baseDomain && window.confirm(`Change the default domain to ${domain}? Every short site name moves with it.`))
              after(runCmd(`Default domain is now ${domain}`, "/api/default-domain", { domain }));
          }}>
            <div class="field-row">
              <Field label="Domain" hint={`Site folders without a dot, like "blog", are its subdomains. Changing it moves every app's *.${d().baseDomain}.`}><input class="input" name="domain" value={d().baseDomain} required /></Field>
              <button class="btn btn-secondary">Change</button>
            </div>
          </form>
        </section>

        <Sheet open={allowing()} title="Allow a domain" onClose={() => setAllowing(false)}
          footer={<><button class="btn btn-secondary" onClick={() => setAllowing(false)}>Cancel</button><button class="btn" type="submit" form="allow">Allow</button></>}>
          <form id="allow" onSubmit={(e) => {
            e.preventDefault();
            const f = Object.fromEntries(new FormData(e.currentTarget));
            setAllowing(false);
            after(runCmd(`Allowed ${grantLabel(String(f.kind), String(f.name))} for ${f.app}`, "/api/grants", { ...f, op: "add" }));
          }}>
            <Field label="App"><select class="select" name="app" required><For each={d().apps}>{(a) => <option>{a}</option>}</For></select></Field>
            <Field label="May use">
              <select class="select" name="kind" onChange={(e) => setKind(e.currentTarget.value)}>
                <option value="domain" selected={kind() === "domain"}>The domain and its subdomains</option>
                <option value="sub" selected={kind() === "sub"}>Subdomains only</option>
                <option value="host" selected={kind() === "host"}>Exactly this hostname</option>
              </select>
            </Field>
            <Field label={kind() === "host" ? "Hostname" : "Domain"}><input class="input" name="name" required placeholder={kind() === "host" ? "blog.client.com" : "client.com"} /></Field>
          </form>
        </Sheet>
      </>)}</Show>
    </>
  );
}
