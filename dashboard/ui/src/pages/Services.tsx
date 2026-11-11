import { For, Show, createResource, createSignal } from "solid-js";
import { api, runCmd } from "../api";
import { Field, StatusBadge } from "../components";

type Iface = { name: string; desc: string; env: string[] };
type Provider = { id: string; interface: string; desc: string; ready: boolean; note?: string };
type Binding = { app: string; interface: string; provider: string; as?: Record<string, string>; env: string[]; ready: boolean };
type Request = { app: string; interface: string; sites: string[] };
type View = { apps: string[]; interfaces: Iface[]; providers: Provider[]; bindings: Binding[]; requests: Request[] };

const label = (id: string) => (id === "postgres" ? "PostgreSQL (built in)" : id.replace(/^app:/, ""));

// "DATABASE_URL=PG_URL, X=Y" -> { DATABASE_URL: "PG_URL", X: "Y" }
const parseAs = (s: string) => {
  const out: Record<string, string> = {};
  for (const part of s.split(/[,\s]+/)) {
    const [from, to] = part.split("=");
    if (from && to) out[from] = to;
  }
  return out;
};

export default function Services() {
  const [data, { refetch }] = createResource(() => api<View>("/api/services"));
  const [iface, setIface] = createSignal("");
  const providersFor = (name: string) => (data()?.providers ?? []).filter((p) => p.interface === name);
  const bind = async (app: string, name: string, provider: string, as: Record<string, string> = {}) => {
    await runCmd(`Bound ${app} to ${name}`, "/api/services/bind", { app, interface: name, provider, set: as });
    refetch();
  };

  return (
    <div class="stack">
      <h1>Services</h1>
      <p class="text-secondary hint">
        Apps ask for services in their <span class="mono">nook.json</span> (<span class="mono">"needs": ["postgres"]</span>); you decide which provider serves them.
        An app only ever sees conventional env vars (like <span class="mono">DATABASE_URL</span>), so it works the same anywhere else.
        Binding restarts the app's running sites.
      </p>
      <Show when={data.error}><div class="alert alert-error">{String(data.error.message)}</div></Show>

      <Show when={data()?.requests.length}>
        <section class="card">
          <h2>Waiting</h2>
          <div class="table-wrap">
            <table class="table">
              <thead><tr><th>App</th><th>Asks for</th><th>From sites</th><th>Provider</th><th /></tr></thead>
              <tbody>
                <For each={data()!.requests}>
                  {(r) => {
                    const ready = () => providersFor(r.interface).filter((p) => p.ready);
                    let sel!: HTMLSelectElement;
                    return (
                      <tr>
                        <td>{r.app}</td>
                        <td class="mono">{r.interface}</td>
                        <td>{r.sites.join(", ")}</td>
                        <td>
                          <Show when={ready().length} fallback={<span class="text-secondary">nothing provides it yet</span>}>
                            <select class="select" ref={sel}><For each={ready()}>{(p) => <option value={p.id}>{label(p.id)}</option>}</For></select>
                          </Show>
                        </td>
                        <td><Show when={ready().length}><button class="btn btn-primary btn-sm" onClick={() => bind(r.app, r.interface, sel.value)}>Grant</button></Show></td>
                      </tr>
                    );
                  }}
                </For>
              </tbody>
            </table>
          </div>
        </section>
      </Show>

      <section class="card">
        <h2>Bindings</h2>
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>App</th><th>Service</th><th>From</th><th>App sees</th><th /></tr></thead>
            <tbody>
              <For each={data()?.bindings} fallback={<tr><td colspan={5} class="text-secondary">No bindings yet.</td></tr>}>
                {(b) => (
                  <tr>
                    <td>{b.app}</td>
                    <td class="mono">{b.interface}</td>
                    <td>{label(b.provider)} <Show when={!b.ready}><StatusBadge ok={false} title="The provider isn't running or no longer offers this">unavailable</StatusBadge></Show></td>
                    <td class="mono">{b.env.join(" ") || "nothing"}</td>
                    <td><button class="btn btn-ghost btn-sm" onClick={async () => {
                      if (!window.confirm(`Unbind ${b.app} from ${b.interface}? Its env goes away and its sites restart.${b.provider === "postgres" ? " The database itself is kept." : ""}`)) return;
                      await runCmd(`Unbound ${b.app} from ${b.interface}`, "/api/services/unbind", { app: b.app, interface: b.interface });
                      refetch();
                    }}>Unbind</button></td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </section>

      <section class="card">
        <h2>Bind an app</h2>
        <form class="form-row" style={{ "margin-top": "0.75rem" }} onSubmit={(e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          bind(String(f.get("app")), String(f.get("interface")), String(f.get("provider")), parseAs(String(f.get("as") || "")));
        }}>
          <Field label="App"><select class="select" name="app" required><For each={data()?.apps}>{(a) => <option>{a}</option>}</For></select></Field>
          <Field label="Service">
            <select class="select" name="interface" required value={iface()} onChange={(e) => setIface(e.currentTarget.value)}>
              <option value="" disabled>choose</option>
              <For each={[...new Set((data()?.providers ?? []).map((p) => p.interface))]}>{(n) => <option>{n}</option>}</For>
            </select>
          </Field>
          <Field label="Provider">
            <select class="select" name="provider" required>
              <For each={providersFor(iface()).filter((p) => p.ready)}>{(p) => <option value={p.id}>{label(p.id)}</option>}</For>
            </select>
          </Field>
          <Field label="Rename (optional)"><input class="input mono" name="as" placeholder="DATABASE_URL=PG_URL" /></Field>
          <button class="btn btn-primary" disabled={!iface()}>Bind</button>
        </form>
        <Show when={iface()}>
          <p class="text-secondary hint">The app gets {(data()?.interfaces.find((i) => i.name === iface())?.env ?? []).map((e) => <span class="mono">{e} </span>)}</p>
        </Show>
      </section>

      <section class="card">
        <h2>Providers</h2>
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>Service</th><th>Provider</th><th>What it is</th><th>State</th></tr></thead>
            <tbody>
              <For each={data()?.providers}>
                {(p) => (
                  <tr>
                    <td class="mono">{p.interface}</td>
                    <td>{label(p.id)}</td>
                    <td>{p.desc}</td>
                    <td><StatusBadge ok={p.ready}>{p.ready ? "ready" : "unavailable"}</StatusBadge> <span class="text-secondary">{p.note}</span></td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
        <p class="text-secondary hint">
          An app becomes a provider by deploying a service with <span class="mono">{'"provides": {"alerts": {"env": {"ALERT_URL": "{url}/alert", "ALERT_TOKEN": "{token}"}}}'}</span> in its nook.json.
          It learns its clients and their tokens from <span class="mono">NOOK_CLIENTS</span>. Known services (postgres, email, alerts, analytics, s3) have fixed env names, so providers of the same service are interchangeable.
        </p>
      </section>
    </div>
  );
}
