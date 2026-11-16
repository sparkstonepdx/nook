import { Show, createResource, createRoot, createSignal, onCleanup } from "solid-js";
import { api } from "./api";
import { bytes } from "./format";

// Each app's memory and disk over the last week, with where they're heading, shown inside its row on the Apps page.
// One request for every app (the headroom view at a week), refreshed every minute while the page is open.
type Point = { t: number; v: number };
type DiskParts = { total: number; sites: number; builds: number; data: number; logs: number; store: number; at: number };
export type AppTrend = {
  app: string; mem: number; limit: number; ownLimit: boolean; cpu: number; perDay: number; daysLeft: number; fullAt: number; kills: number;
  points: Point[]; projection: Point[];
  disk: number; diskParts?: DiskParts; diskFree: number; diskPerDay: number; diskDaysLeft: number; diskFullAt: number; diskPoints: Point[]; diskProjection: Point[];
};

// Fetched when the Apps page opens, not at startup (which may be the login page), and kept across pages.
const [on, setOn] = createSignal(false);
const [trends, { refetch }] = createRoot(() =>
  createResource(on, () => api<{ apps?: AppTrend[] }>("/api/headroom?range=7d").then((h) => h.apps ?? []).catch(() => [] as AppTrend[])),
);
export const trendFor = (app: string) => trends()?.find((t) => t.app === app);

// Loads the figures now and keeps them fresh while the calling page is open.
export function refreshTrends() {
  if (on()) refetch();
  else setOn(true);
  const timer = setInterval(() => !document.hidden && refetch(), 60_000);
  onCleanup(() => clearInterval(timer));
}

const dayText = (unix: number) => new Date(unix * 1000).toLocaleDateString([], { weekday: "short", month: "short", day: "numeric" });

// A week as a line, with an optional limit (dashed hairline) and the projection (dashed, up to a week ahead).
function Spark(props: { points: Point[]; projection: Point[]; limitMB?: number; label: string }) {
  const W = 300, H = 48;
  const t0 = () => props.points[0]?.t ?? 0;
  const t1 = () => Math.max(props.points[props.points.length - 1]?.t ?? 1, props.projection[1]?.t ?? 0);
  const top = () => {
    const peak = Math.max(1, ...[...props.points, ...props.projection].map((p) => p.v));
    return props.limitMB ? Math.max(props.limitMB, peak) * 1.05 : peak * 1.25;
  };
  const x = (t: number) => ((t - t0()) / Math.max(1, t1() - t0())) * W;
  const y = (v: number) => H - 2 - (v / top()) * (H - 4);
  const path = (ps: Point[]) => ps.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join("");
  return (
    <svg class="spark" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={props.label}>
      <Show when={props.limitMB}><line class="limit" x1="0" x2={W} y1={y(props.limitMB!)} y2={y(props.limitMB!)} /></Show>
      <path class="line" d={path(props.points)} />
      <Show when={props.projection.length === 2}><path class="projection" d={path(props.projection)} /></Show>
    </svg>
  );
}

// Opening an app measures its disk on the spot (at most once a minute), so files just deleted show.
const measured = new Map<string, number>();
async function measureDisk(app: string) {
  if (Date.now() - (measured.get(app) ?? 0) < 60_000) return;
  measured.set(app, Date.now());
  try { await api(`/api/apps/${app}/disk`, {}); refetch(); } catch { /* the next pass catches up */ }
}

export function AppCharts(props: { app: string; onLimits: (app: string) => void }) {
  measureDisk(props.app);
  const t = () => trendFor(props.app);
  const memSoon = () => (t()?.daysLeft ?? -1) >= 0 && t()!.daysLeft <= 14;
  const diskSoon = () => (t()?.diskDaysLeft ?? -1) >= 0 && t()!.diskDaysLeft <= 30;
  const memTrend = () => {
    const a = t()!;
    if (a.perDay <= 0) return "Steady since it last restarted";
    const rate = `Up ${bytes(a.perDay)} a day since it last restarted`;
    if (a.daysLeft < 0) return rate;
    return `${rate} · ${a.ownLimit ? "reaches its limit" : "would fill the server's memory"} ${a.daysLeft < 1 ? "within a day" : `around ${dayText(a.fullAt)}`}`;
  };
  const diskTrend = () => {
    const a = t()!;
    if (a.diskPerDay <= 0) return a.diskPoints.length > 1 ? "Steady this week" : "Measured every 15 minutes; the trend shows after a day";
    const rate = `Up ${bytes(a.diskPerDay)} a day this week`;
    if (a.diskDaysLeft < 0 || a.diskDaysLeft > 365) return rate; // a date years out says nothing
    return `${rate} · the disk is full ${a.diskDaysLeft < 1 ? "within a day" : `around ${dayText(a.diskFullAt)}`} at this rate`;
  };
  const parts = () => {
    const p = t()?.diskParts;
    if (!p) return "";
    return ([["sites", p.sites], ["builds", p.builds], ["data", p.data], ["logs", p.logs], ["rollback copies", p.store]] as const)
      .filter(([, n]) => n >= 1 << 20).map(([k, n]) => `${k} ${bytes(n)}`).join(" · ");
  };
  return (
    <Show when={t()}>
      {(a) => (
        <div class="app-charts">
          <figure class="app-chart" classList={{ soon: memSoon() }}>
            <figcaption>
              <span>Memory <small class="figures">{bytes(a().mem)}{a().ownLimit ? ` of ${bytes(a().limit)}` : ""} · {a().cpu}% CPU</small></span>
              <small class={memSoon() ? "trend-soon" : ""}>{memTrend()}<Show when={a().kills > 0}> · stopped {a().kills} {a().kills === 1 ? "time" : "times"} by its limit this week</Show></small>
            </figcaption>
            <Show when={a().points.length > 1}><Spark points={a().points} projection={a().projection} limitMB={a().ownLimit ? a().limit / (1 << 20) : undefined} label={`${a().app} memory over the last week: ${memTrend()}`} /></Show>
            <Show when={memSoon() && a().ownLimit}><button class="btn btn-secondary btn-sm" onClick={() => props.onLimits(a().app)}>Change limit</button></Show>
          </figure>
          <Show when={a().disk > 0}>
            <figure class="app-chart" classList={{ soon: diskSoon() }}>
              <figcaption>
                <span>Disk <small class="figures">{bytes(a().disk)} · {bytes(a().diskFree)} free on the server</small></span>
                <small class={diskSoon() ? "trend-soon" : ""}>{diskTrend()}</small>
                <Show when={parts()}><small>{parts()}</small></Show>
              </figcaption>
              <Show when={a().diskPoints.length > 1}><Spark points={a().diskPoints} projection={a().diskProjection} label={`${a().app} disk over the last week: ${diskTrend()}`} /></Show>
            </figure>
          </Show>
        </div>
      )}
    </Show>
  );
}
