import { For, Show, createMemo, createResource, createSignal, onCleanup } from "solid-js";
import { api, runCmd, say } from "../api";
import { Field, StatusBadge } from "../components";

type Resource = { name: string; status: "ok" | "watch" | "tight"; used: number; figure: string; detail: string };
type Advice = { level: "watch" | "tight"; title: string; detail: string; app?: string };
type Point = { t: number; v: number };
type Series = { name: string; unit: string; points: Point[]; max: number };
type SubductionId = { installed: boolean; url?: string; peerId?: string; peerIdHex?: string; card?: string; problem?: string };
type Headroom = { status: "ok" | "watch" | "tight" | "learning"; summary: string; resources: Resource[]; advice: Advice[]; series: Series[]; since?: number; samples: number };

const statusWord = { ok: "plenty", watch: "watch", tight: "tight" } as const;

// The Server section: one sentence on how much room is left, a meter per resource, advice when there's
// something to add, and how memory, CPU and disk moved over a day, a week or a month.
export default function Server(props: { caddy: string; onLimits: (app: string) => void; after: (p?: Promise<unknown>) => void }) {
  const [range, setRange] = createSignal<"24h" | "7d" | "30d">("24h");
  const [data, { refetch }] = createResource(range, (r) => api<Headroom>(`/api/headroom?range=${r}`));
  const timer = setInterval(() => !document.hidden && refetch(), 60_000);
  onCleanup(() => clearInterval(timer));
  // keep the last figures on screen while a new range loads: no flash, no jump
  const view = createMemo<Headroom | undefined>((prev) => data() ?? prev);
  const [hover, setHover] = createSignal<number | null>(null); // a time, shared by the three charts

  return (
    <section class="section server">
      <div class="page-head">
        <h2>Server</h2>
        <Show when={props.caddy !== "active"}><StatusBadge ok={false}>caddy {props.caddy}</StatusBadge></Show>
        <div class="page-actions">
          <button class="btn btn-secondary btn-sm" onClick={() => props.after(runCmd("Synced all apps", "/api/sync-all"))}>Sync all</button>
          <button class="btn btn-secondary btn-sm" onClick={() => props.after(runCmd("Reloaded Caddy", "/api/caddy/reload"))}>Reload Caddy</button>
        </div>
      </div>
      <Show when={view()} fallback={<p class="empty">Measuring…</p>}>
        {(v) => (
          <div class="stack">
            <p class="server-summary"><span class={`status-dot status-${v().status}`} aria-hidden="true" />{v().summary}</p>

            <Show when={v().resources.length}>
              <ul class="list meters">
                <For each={v().resources}>
                  {(r) => (
                    <li class="row meter-row" title={r.detail}>
                      <span class="meter-name">{r.name}</span>
                      <span class={`meter meter-${r.status}`} role="meter" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(r.used * 100)} aria-label={`${r.name} ${r.figure}`}>
                        <span style={{ width: `${Math.min(100, Math.max(1.5, r.used * 100))}%` }} />
                      </span>
                      <span class="meter-figure">{r.figure}<Show when={r.status !== "ok"}><StatusBadge warn={r.status === "watch"} ok={r.status === "tight" ? false : undefined}>{statusWord[r.status]}</StatusBadge></Show></span>
                    </li>
                  )}
                </For>
              </ul>
            </Show>

            <Show when={v().advice.length}>
              <ul class="list advice">
                <For each={v().advice}>
                  {(a) => (
                    <li class="row">
                      <span class={`status-dot status-${a.level}`} aria-hidden="true" />
                      <span class="row-main"><span>{a.title}</span><small>{a.detail}</small></span>
                      <Show when={a.app}><span class="row-end"><button class="btn btn-secondary btn-sm" onClick={() => props.onLimits(a.app!)}>Change limit</button></span></Show>
                    </li>
                  )}
                </For>
              </ul>
            </Show>

            <Show when={v().series.length && v().series[0].points.length > 1}>
              <div class="chart-head">
                <span class="eyebrow">History</span>
                <div class="seg" role="group" aria-label="Range">
                  <For each={["24h", "7d", "30d"] as const}>{(r) => <button type="button" aria-pressed={range() === r} classList={{ on: range() === r }} onClick={() => setRange(r)}>{{ "24h": "Day", "7d": "Week", "30d": "Month" }[r]}</button>}</For>
                </div>
              </div>
              <div class="charts" classList={{ loading: data.loading }}>
                <For each={v().series}>{(s) => <LineChart series={s} hover={hover()} setHover={setHover} range={range()} />}</For>
              </div>
              <ChartTable series={v().series} />
            </Show>
            <Show when={v().status !== "learning" && v().samples < 12}><p class="hint">The graph fills in as nook takes a reading every 5 minutes.</p></Show>
          </div>
        )}
      </Show>
      <Subduction />
    </section>
  );
}

// What an app needs to reach this server's Subduction relay with Keyhive: its address, its peer id and its
// contact card, each with its own Copy. Only shown when the subduction app is installed.
function Subduction() {
  const [id] = createResource(() => api<SubductionId>("/api/subduction").catch(() => undefined));
  const copy = (text: string, what: string) => { navigator.clipboard?.writeText(text); say(`${what} copied`); };
  const env = (v: SubductionId) =>
    [`SUBDUCTION_URL=${v.url}`, `SYNC_SERVER_PEER_ID=${v.peerId}`, v.card && `SYNC_SERVER_CONTACT_CARD=${v.card}`].filter(Boolean).join("\n");
  const line = (label: string, value: string, what: string, hint?: string) => (
    <Field label={label} hint={hint}>
      <div class="field-row">
        <input class="input mono" readonly value={value} onFocus={(e) => e.currentTarget.setSelectionRange(0, e.currentTarget.value.length)} />
        <button type="button" class="btn btn-secondary" onClick={() => copy(value, what)}>Copy</button>
      </div>
    </Field>
  );
  return (
    <Show when={id()?.installed && id()}>
      {(v) => (
        <div class="stack subduction-id">
          <div class="chart-head">
            <span class="eyebrow">Subduction</span>
            <button type="button" class="btn btn-secondary btn-sm" onClick={() => copy(env(v()), "Settings")}>Copy all</button>
          </div>
          {line("Address", v().url!, "Address")}
          {line("Peer id", v().peerId!, "Peer id", "The server's public key, base64. Apps set it as SYNC_SERVER_PEER_ID.")}
          <Show when={v().card} fallback={<div class="note note-warn"><span>{v().problem}</span></div>}>
            {line("Keyhive contact card", v().card!, "Contact card", "Apps set it as SYNC_SERVER_CONTACT_CARD, to add the server to a doc as a relay.")}
          </Show>
        </div>
      )}
    </Show>
  );
}

const fmtTime = (t: number, range: string) =>
  new Date(t * 1000).toLocaleString(undefined, range === "24h" ? { hour: "numeric", minute: "2-digit" } : { month: "short", day: "numeric", hour: range === "7d" ? "numeric" : undefined });

// One series over time: a 2px line over a light area, a hairline grid at 0/50/100%, and a crosshair that
// snaps to the nearest reading and is shared with the other charts.
function LineChart(props: { series: Series; hover: number | null; setHover: (t: number | null) => void; range: string }) {
  const [W, setW] = createSignal(320);
  const H = 104, padL = 30, padR = 4, padT = 8, padB = 18;
  const pts = () => props.series.points;
  const t0 = () => pts()[0]?.t ?? 0, t1 = () => pts()[pts().length - 1]?.t ?? 1;
  const x = (t: number) => padL + ((t - t0()) / Math.max(1, t1() - t0())) * (W() - padL - padR);
  const y = (v: number) => padT + (1 - Math.min(props.series.max, Math.max(0, v)) / props.series.max) * (H - padT - padB);
  const line = () => pts().map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join("");
  const area = () => `${line()}L${x(t1()).toFixed(1)},${y(0)}L${x(t0()).toFixed(1)},${y(0)}Z`;
  const nearest = () => {
    const h = props.hover;
    if (h == null || !pts().length) return null;
    return pts().reduce((a, b) => (Math.abs(b.t - h) < Math.abs(a.t - h) ? b : a));
  };
  const last = () => pts()[pts().length - 1];
  let svg!: SVGSVGElement;
  const move = (e: PointerEvent) => {
    const r = svg.getBoundingClientRect();
    const px = e.clientX - r.left;
    const t = t0() + ((px - padL) / (W() - padL - padR)) * (t1() - t0());
    props.setHover(Math.max(t0(), Math.min(t1(), t)));
  };
  return (
    <figure class="chart">
      <figcaption>
        <span>{props.series.name}</span>
        <strong>{(nearest() ?? last())?.v.toFixed(0)}{props.series.unit}</strong>
        <Show when={nearest()}>{(n) => <small>{fmtTime(n().t, props.range)}</small>}</Show>
      </figcaption>
      <svg ref={(el) => { svg = el; const ro = new ResizeObserver(() => setW(Math.max(160, el.clientWidth))); ro.observe(el); onCleanup(() => ro.disconnect()); }} viewBox={`0 0 ${W()} ${H}`} height={H} role="img" aria-label={`${props.series.name}, ${fmtTime(t0(), props.range)} to ${fmtTime(t1(), props.range)}`}
        onPointerMove={move} onPointerDown={move} onPointerLeave={() => props.setHover(null)}>
        <For each={[0, 50, 100]}>{(g) => <><line class="grid" x1={padL} x2={W() - padR} y1={y(g)} y2={y(g)} /><text class="tick" x={padL - 6} y={y(g) + 3}>{g}%</text></>}</For>
        <path class="area" d={area()} />
        <path class="line" d={line()} />
        <Show when={nearest()}>{(n) => <>
          <line class="crosshair" x1={x(n().t)} x2={x(n().t)} y1={padT} y2={H - padB} />
          <circle class="dot" cx={x(n().t)} cy={y(n().v)} r="3.5" />
        </>}</Show>
        <text class="tick start" x={padL} y={H - 4}>{fmtTime(t0(), props.range)}</text>
        <text class="tick" x={W() - padR} y={H - 4}>now</text>
      </svg>
    </figure>
  );
}

// The same figures as a table, for screen readers and anyone who wants the numbers.
function ChartTable(props: { series: Series[] }) {
  const rows = () => {
    const s = props.series;
    const n = s[0]?.points.length ?? 0;
    const step = Math.max(1, Math.floor(n / 12));
    const out: { t: number; v: number[] }[] = [];
    for (let i = n - 1; i >= 0; i -= step) out.push({ t: s[0].points[i].t, v: s.map((x) => x.points[i]?.v ?? NaN) });
    return out;
  };
  return (
    <details class="chart-table">
      <summary>Figures as a table</summary>
      <div class="table-wrap">
        <table class="table">
          <thead><tr><th>Time</th><For each={props.series}>{(s) => <th>{s.name}</th>}</For></tr></thead>
          <tbody><For each={rows()}>{(r) => <tr><td>{new Date(r.t * 1000).toLocaleString()}</td><For each={r.v}>{(v) => <td class="num">{isNaN(v) ? "" : `${v.toFixed(0)}%`}</td>}</For></tr>}</For></tbody>
        </table>
      </div>
    </details>
  );
}
