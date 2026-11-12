import { For, Show, createResource, createSignal } from "solid-js";
import { api, runCmd } from "../api";
import { Field, PageHead, Sheet, StatusBadge } from "../components";

type Iface = { name: string; desc: string; env: string[] };
type Provider = { id: string; interface: string; desc: string; ready: boolean; note?: string };
type Binding = { app: string; interface: string; provider: string; as?: Record<string, string>; env: string[]; ready: boolean };
type Request = { app: string; interface: string; sites: string[] };
type View = { apps: string[]; interfaces: Iface[]; providers: Provider[]; bindings: Binding[]; requests: Request[] };

const label = (id: string) => id.replace(/^app:/, "");

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
  const [binding, setBinding] = createSignal(false);
  const [iface, setIface] = createSignal("");
  const providersFor = (name: string) => (data()?.providers ?? []).filter((p) => p.interface === name);
  const bind = async (app: string, name: string, provider: string, as: Record<string, string> = {}) => {
    await runCmd(`Bound ${app} to ${name}`, "/api/services/bind", { app, interface: name, provider, set: as });
    refetch();
  };

  return (
    <>
      <PageHead title="Services">
        <button class="btn" onClick={() => setBinding(true)}>Bind an app</button>
      </PageHead>
      <p class="lede">Apps ask for services in their <span class="mono">nook.json</span>, and you pick who provides them. An app only sees ordinary env vars like <span class="mono">DATABASE_URL</span>, so it runs the same anywhere.</p>
      <Show when={data.error}><div class="note note-bad">{String(data.error!.message)}</div></Show>

      <Show when={data()?.requests.length}>
        <section class="section">
          <h2>Waiting for you</h2>
          <ul class="list">
            <For each={data()!.requests}>
              {(r) => {
                const ready = () => providersFor(r.interface).filter((p) => p.ready);
                let sel!: HTMLSelectElement;
                return (
                  <li class="row">
                    <span class="row-main"><span>{r.app} needs <span class="mono">{r.interface}</span></span><small>for {r.sites.join(", ")}</small></span>
                    <span class="row-end">
                      <Show when={ready().length} fallback={<span>nothing provides it yet</span>}>
                        <select class="select" ref={sel} aria-label="Provider"><For each={ready()}>{(p) => <option value={p.id}>{label(p.id)}</option>}</For></select>
                        <button class="btn btn-sm" onClick={() => bind(r.app, r.interface, sel.value)}>Grant</button>
                      </Show>
                    </span>
                  </li>
                );
              }}
            </For>
          </ul>
        </section>
      </Show>

      <section class="section">
        <h2>Bindings</h2>
        <ul class="list">
          <For each={data()?.bindings} fallback={<li class="row"><span class="empty">No bindings yet.</span></li>}>
            {(b) => (
              <li class="row">
                <span class="row-main">
                  <span>{b.app} <span class="text-secondary">uses</span> {b.interface}</span>
                  <small>from {label(b.provider)} · sees <span class="mono">{b.env.join(" ") || "nothing"}</span></small>
                </span>
                <span class="row-end">
                  <Show when={!b.ready}><StatusBadge ok={false} title="The provider isn't running or no longer offers this">unavailable</StatusBadge></Show>
                  <button class="btn btn-ghost btn-sm" onClick={async () => {
                    if (!window.confirm(`Unbind ${b.app} from ${b.interface}? Its env goes away and its sites restart.`)) return;
                    await runCmd(`Unbound ${b.app} from ${b.interface}`, "/api/services/unbind", { app: b.app, interface: b.interface });
                    refetch();
                  }}>Unbind</button>
                </span>
              </li>
            )}
          </For>
        </ul>
      </section>

      <section class="section">
        <h2>Providers</h2>
        <ul class="list">
          <For each={data()?.providers}>
            {(p) => (
              <li class="row">
                <span class="row-main"><span>{label(p.id)} <span class="text-secondary">provides</span> {p.interface}</span><small>{p.desc}<Show when={p.note}> · {p.note}</Show></small></span>
                <span class="row-end"><Show when={!p.ready}><StatusBadge ok={false}>unavailable</StatusBadge></Show></span>
              </li>
            )}
          </For>
        </ul>
        <p class="hint">
          An app becomes a provider by deploying a service with <span class="mono">"provides"</span> in its nook.json, and learns its clients from <span class="mono">NOOK_CLIENTS</span>.
          Known services (postgres, email, alerts, analytics, s3) have fixed env names, so their providers are interchangeable.
        </p>
      </section>

      <Sheet open={binding()} title="Bind an app" onClose={() => setBinding(false)}
        footer={<><button class="btn btn-secondary" onClick={() => setBinding(false)}>Cancel</button><button class="btn" type="submit" form="bind" disabled={!iface()}>Bind</button></>}>
        <form id="bind" onSubmit={(e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          setBinding(false);
          bind(String(f.get("app")), String(f.get("interface")), String(f.get("provider")), parseAs(String(f.get("as") || "")));
        }}>
          <Field label="App"><select class="select" name="app" required><For each={data()?.apps}>{(a) => <option>{a}</option>}</For></select></Field>
          <div class="field-row">
            <Field label="Service">
              <select class="select" name="interface" required onChange={(e) => setIface(e.currentTarget.value)}>
                <option value="" disabled selected={!iface()}>Choose</option>
                <For each={[...new Set((data()?.providers ?? []).map((p) => p.interface))]}>{(n) => <option selected={n === iface()}>{n}</option>}</For>
              </select>
            </Field>
            <Field label="From">
              <select class="select" name="provider" required>
                <For each={providersFor(iface()).filter((p) => p.ready)}>{(p) => <option value={p.id}>{label(p.id)}</option>}</For>
              </select>
            </Field>
          </div>
          <Show when={iface()}>
            <p class="hint">The app gets {(data()?.interfaces.find((i) => i.name === iface())?.env ?? []).join(", ")}. Binding restarts its running sites.</p>
          </Show>
          <Field label="Rename" hint="Optional, for an app that already uses its own names."><input class="input mono" name="as" placeholder="DATABASE_URL=PG_URL" /></Field>
        </form>
      </Sheet>
    </>
  );
}
