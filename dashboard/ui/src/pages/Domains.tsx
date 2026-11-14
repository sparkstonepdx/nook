import { For, Show, createResource, createSignal } from "solid-js";
import { api, runCmd } from "../api";
import { Field, PageHead, Sheet, StatusBadge, Confirm, ConfirmStrip } from "../components";
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
  const [moving, setMoving] = createSignal(""); // the default domain waiting on "Change?"

  return (
    <>
      <PageHead title="Domains" detail={data()?.baseDomain}>
        <button class="btn" onClick={() => setAllowing(true)}>Allow a domain</button>
      </PageHead>
      <Show when={data.error}><div class="note note-bad">{String(data.error!.message)}</div></Show>
      <Certificates />
      <Show when={data()}>{(d) => (<>
        <section class="section">
          <h2>What each app may use</h2>
          <ul class="list">
            <For each={d().grants} fallback={<li class="row"><span class="empty">Nothing allowed yet.</span></li>}>
              {(g) => (
                <li class="row">
                  <span class="row-main"><span class="mono">{grantLabel(g.kind, g.name)}</span><small>{g.app} · {kindLabel[g.kind]}</small></span>
                  <span class="row-end">
                    <Confirm label="Remove" yes="Remove" ask={`Remove ${grantLabel(g.kind, g.name)} from ${g.app}? Its sites on it go offline.`} onYes={() => after(runCmd(`Removed ${grantLabel(g.kind, g.name)} from ${g.app}`, "/api/grants", { ...g, op: "remove" }))} />
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
                  <Confirm label="Turn off" yes="Turn off" disabled={!d().dashboardHost} title={d().dashboardHost ? "" : "Set a dashboard address first"}
                    ask="Turn off IP access? If DNS for the dashboard address breaks, you'll need SSH to get back in." onYes={() => after(runCmd("IP access off", "/api/ip-access", { on: false }))} />
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
            const domain = String(new FormData(e.currentTarget).get("domain") || "").trim();
            if (domain !== d().baseDomain) setMoving(domain);
          }}>
            <div class="field-row">
              <Field label="Domain" hint={`Site folders without a dot, like "blog", are its subdomains. Changing it moves every app's *.${d().baseDomain}.`}><input class="input" name="domain" value={d().baseDomain} required /></Field>
              <button class="btn btn-secondary">Change</button>
            </div>
            <Show when={moving()}>{(to) => (
              <ConfirmStrip ask={`Change the default domain to ${to()}? Every short site name moves with it.`} yes="Change"
                onNo={() => setMoving("")} onYes={() => { const domain = to(); setMoving(""); after(runCmd(`Default domain is now ${domain}`, "/api/default-domain", { domain })); }} />
            )}</Show>
          </form>
        </section>

        <NotFoundPage />

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

type Nested = { domain: string; status: string; detail?: string; expires?: string; retry?: string };
type CertDomain = { domain: string; for: string[] | null; wildcard: boolean; provider?: string; status?: string; detail?: string; expires?: string; retry?: string; nested?: Nested[] };
type Provider = { id: string; name: string; module: string; package: string; hint: string; tokenUrl?: string; steps?: string[] };
type Certs = { caddy: { version: string; preferWildcard: boolean; held: boolean; modules: Record<string, boolean> }; domains: CertDomain[] | null; providers: Provider[] };

const statusLabel: Record<string, string> = { issued: "issued", waiting: "waiting", limited: "rate limited", error: "error" };
const pending = (s?: string) => s === "waiting" || s === "limited";
// When Let's Encrypt allows the next try, in the viewer's own time: "Sat, Nov 14, 12:21 AM".
const nextTry = (iso: string) => new Date(iso).toLocaleString([], { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });

// the zone a domain most likely is: its last two labels (apps.example.com is in example.com). Only a hint in the
// steps: nook finds the real zone when it checks the token.
const zoneOf = (d: string) => d.split(".").slice(-2).join(".");

// who uses a domain, short: "default domain and 18 apps", "wordbag and kinlog"
function usedBy(f: string[] | null) {
  if (!f?.length) return "no longer used by any app";
  const dflt = f.includes("default domain");
  const apps = f.filter((x) => x !== "default domain");
  const named = apps.length <= 2 ? apps.join(" and ") : `${apps.length} apps`;
  return dflt ? (apps.length ? `default domain and ${named}` : "default domain") : named;
}

// Wildcard certificates: one *.domain certificate by DNS challenge instead of one per hostname, so apps and PR
// previews don't use up Let's Encrypt's weekly limit.
function Certificates() {
  const [certs, { refetch }] = createResource(() => api<Certs>("/api/certificates"));
  const [setting, setSetting] = createSignal<CertDomain | null>(null);
  const [provider, setProvider] = createSignal("cloudflare");
  const after = async (p?: Promise<unknown>) => { await p; refetch(); };
  const prov = () => certs()?.providers.find((p) => p.id === provider());
  const hasPlugin = (id: string) => !!certs()?.caddy.modules[id];
  const without = () => (certs()?.domains ?? []).filter((d) => !d.wildcard).map((d) => d.domain);
  const anyWildcard = () => (certs()?.domains ?? []).some((d) => d.wildcard);

  return (
    <section class="section">
      <h2>Certificates</h2>
      <Show when={certs.error}><div class="note note-bad">{String(certs.error!.message)}</div></Show>
      <Show when={certs.loading && !certs()}><p class="hint">Checking Caddy…</p></Show>
      <Show when={certs()}>{(c) => (<div class="stack">
        <Show when={without().length}>
          <div class="note note-warn"><span>Use a wildcard for <strong>{without().join(", ")}</strong>. Every app and preview on a domain otherwise gets its own certificate, and Let's Encrypt allows 50 a week per domain.</span></div>
        </Show>
        <ul class="list">
          <For each={c().domains ?? []}>
            {(d) => (
              <li class="cert-item"><div class="row">
                <span class="row-main">
                  <span><span class="mono">{d.wildcard ? `*.${d.domain}` : d.domain}</span><Show when={d.wildcard}> <StatusBadge ok={d.status === "issued"} warn={pending(d.status)}>{statusLabel[d.status ?? ""] ?? d.status}</StatusBadge></Show></span>
                  <small>{usedBy(d.for)} · {d.wildcard ? `wildcard via ${c().providers.find((p) => p.id === d.provider)?.name ?? d.provider}` : "one certificate per hostname"}
                    <Show when={d.expires}> · valid until {new Date(d.expires!).toLocaleDateString()}</Show></small>
                  <Show when={d.retry}><small class="next-try">Next try: {nextTry(d.retry!)}</small></Show>
                  <Show when={d.status === "error" || pending(d.status)}><small class={d.status === "error" ? "text-error" : ""}>{d.detail}</small></Show>

                </span>
                <span class="row-end">
                  <Show when={d.wildcard} fallback={<button class="btn btn-sm" onClick={() => setSetting(d)}>Use a wildcard</button>}>
                    <button class="btn btn-ghost btn-sm" onClick={() => setSetting(d)}>Token</button>
                    <Confirm label="Remove" yes="Remove" ask={`Stop using a wildcard for ${d.domain}? Each hostname gets its own certificate again, which counts against the weekly limit.`} onYes={() => after(runCmd(`${d.domain}: one certificate per hostname`, `/api/certificates/${encodeURIComponent(d.domain)}/remove`, {}))} />
                  </Show>
                </span>
              </div>
              <Show when={d.nested?.length}>
                <ul class="nested-certs" aria-label={`Wildcards under ${d.domain}`}>
                  <For each={d.nested}>
                    {(n) => (
                      <li>
                        <span class="mono">*.{n.domain}</span>
                        <StatusBadge ok={n.status === "issued"} warn={pending(n.status)}>{statusLabel[n.status] ?? n.status}</StatusBadge>
                        <Show when={n.status === "error"}><small class="text-error">{n.detail}</small></Show>
                      </li>
                    )}
                  </For>
                </ul>
              </Show>
              </li>
            )}
          </For>
        </ul>
        <p class="hint">A wildcard covers one level: <span class="mono">blog.example.com</span> and PR previews (<span class="mono">blog-pr-12.example.com</span>). A site deeper down, like <span class="mono">a.b.example.com</span>, gets a wildcard for its parent (<span class="mono">*.b.example.com</span>) by itself, shared by everything at that level. The domain itself keeps its own certificate. Until a wildcard is issued, the sites under it keep their own. If Let's Encrypt's weekly limit is reached, new sites wait for the wildcard instead, so it's first in line.</p>
        <ul class="list">
          <li class="row">
            <span class="row-main">
              <span>Caddy <span class="mono">{c().caddy.version || "?"}</span><Show when={c().caddy.held}> <StatusBadge>held</StatusBadge></Show></span>
              <small>{c().providers.map((p) => `${p.name} plugin: ${hasPlugin(p.id) ? "added" : "not added"}`).join(" · ")}<Show when={c().caddy.held}> · apt won't replace it, so update it here</Show></small>
              <Show when={anyWildcard() && !c().caddy.preferWildcard}><small class="text-error">Caddy {c().caddy.version} can't reuse a wildcard for each app's own address; that needs 2.10 or later. Update Caddy.</small></Show>
            </span>
            <span class="row-end">
              <Show when={c().caddy.held}><Confirm label="Update Caddy" yes="Update" ask="Update Caddy, keeping its plugins? Sites pause for a few seconds while it restarts." onYes={() => after(runCmd("Updated Caddy", "/api/caddy/upgrade", {}))} /></Show>
            </span>
          </li>
        </ul>
      </div>)}</Show>

      <Sheet open={!!setting()} title={setting()?.wildcard ? `Token for *.${setting()?.domain}` : `Wildcard for ${setting()?.domain ?? ""}`} onClose={() => setSetting(null)}
        footer={<><button class="btn btn-secondary" onClick={() => setSetting(null)}>Cancel</button><button class="btn" type="submit" form="wildcard" disabled={!hasPlugin(provider())}>Save</button></>}>
        <Show when={setting()}>{(d) => (
          <form id="wildcard" class="stack" onSubmit={(e) => {
            e.preventDefault();
            const f = new FormData(e.currentTarget);
            const domain = d().domain;
            setSetting(null);
            after(runCmd(`*.${domain}: wildcard on`, "/api/certificates", { domain, provider: provider(), token: String(f.get("token") || "") }));
          }}>
            <p class="hint">Caddy proves it controls <span class="mono">{d().domain}</span> through your DNS host, then gets one certificate for <span class="mono">*.{d().domain}</span>. Its DNS has to be at a provider below.</p>
            <Field label="DNS provider">
              <select class="select" onChange={(e) => setProvider(e.currentTarget.value)}>
                <For each={certs()?.providers}>{(p) => <option value={p.id} selected={p.id === provider()}>{p.name}</option>}</For>
              </select>
            </Field>
            <Show when={hasPlugin(provider())} fallback={
              <div class="stack">
                <div class="note note-warn"><span>Caddy needs the {prov()?.name} plugin first. Adding it downloads a Caddy build with it, and holds the package so an apt upgrade can't drop it.</span></div>
                <button type="button" class="btn btn-secondary" onClick={() => after(runCmd(`Added the ${prov()?.name} plugin to Caddy`, "/api/caddy/plugin", { provider: provider() }))}>Add the {prov()?.name} plugin to Caddy</button>
              </div>
            }>
              <Show when={prov()?.steps?.length}>
                <div class="token-steps">
                  <strong>Make a {prov()?.name} token</strong>
                  <ol>
                    <For each={prov()!.steps}>{(step) => <li>{step.replace("{zone}", zoneOf(d().domain))}</li>}</For>
                  </ol>
                  <Show when={prov()?.tokenUrl}><a class="btn btn-secondary btn-sm" href={prov()!.tokenUrl} target="_blank" rel="noopener">Open {prov()?.name}'s API Tokens page ↗</a></Show>
                </div>
              </Show>
              <Field label="API token" hint={prov()?.hint}>
                <input class="input mono" name="token" type="password" autocomplete="off" required />
              </Field>
              <p class="hint">nook checks the token can see this domain's zone before saving. It's kept in a file only root can read, and Caddy restarts to pick it up.</p>
            </Show>
          </form>
        )}</Show>
      </Sheet>
    </section>
  );
}

type NotFound = { html: string; custom: boolean; max: number };

// The server's 404 page: what a static site without its own 404.html shows for a path it doesn't have.
function NotFoundPage() {
  const [page, { refetch }] = createResource(() => api<NotFound>("/api/not-found"));
  const [draft, setDraft] = createSignal<string | null>(null);
  const text = () => draft() ?? page()?.html ?? "";
  const tooBig = () => new Blob([text()]).size > (page()?.max ?? Infinity);
  const save = async (html: string, title: string) => {
    await runCmd(title, "/api/not-found", { html });
    setDraft(null);
    refetch();
  };

  return (
    <section class="section">
      <h2>404 page <Show when={page()}>{(p) => <StatusBadge ok={!p().custom}>{p().custom ? "edited" : "default"}</StatusBadge>}</Show></h2>
      <Show when={page.error}><div class="note note-bad">{String(page.error!.message)}</div></Show>
      <form class="card stack" onSubmit={(e) => { e.preventDefault(); save(text(), "Saved the 404 page"); }}>
        <Field label="HTML" hint={`Shown by static sites that don't have their own 404.html. One self-contained file, up to ${Math.round((page()?.max ?? 0) / 1024)} KB: inline any styles and images. Takes effect right away: open a missing path on any static site to see it.`}>
          <textarea class="textarea mono" rows={14} spellcheck={false} autocomplete="off" value={text()} onInput={(e) => setDraft(e.currentTarget.value)} />
        </Field>
        <Show when={tooBig()}><div class="note note-bad">Too big: keep it under {Math.round((page()?.max ?? 0) / 1024)} KB.</div></Show>
        <div class="field-row">
          <button class="btn btn-secondary" disabled={draft() === null || tooBig()}>Save</button>
          <Show when={page()?.custom}>
            <Confirm class="btn btn-ghost" label="Restore default" yes="Restore" ask="Put the default 404 page back? Your edits are lost." onYes={() => save("", "Restored the default 404 page")} />
          </Show>
        </div>
      </form>
    </section>
  );
}
