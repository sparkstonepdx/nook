import { For, Show, createSignal, onCleanup } from "solid-js";
import { api } from "./api";

// What a store install or update is doing, from /api/store/jobs. The server writes it as it goes, so a job the
// daily check started, or one whose sheet was closed, shows up all the same.
export type StoreJob = {
  app: string; action: "install" | "update"; name: string; from?: string; to: string;
  step: string; bytes?: number; total?: number; started: string; updated: string; done?: boolean; error?: string;
};

const [jobs, setJobs] = createSignal<StoreJob[]>([]);
export { jobs };
export const jobFor = (app: string) => jobs().find((j) => j.app === app);
export const running = (j?: StoreJob) => !!j && !j.done;

export async function loadJobs() {
  try { setJobs((await api<{ jobs: StoreJob[] }>("/api/store/jobs")).jobs); } catch { /* the next poll tries again */ }
}

// Polls while the page that calls it is open: every second while something runs (or `busy` says one is
// starting), every 10 otherwise.
export function pollJobs(busy: () => boolean) {
  let stop = false;
  let timer: number | undefined;
  const tick = async () => {
    await loadJobs();
    if (stop) return;
    timer = window.setTimeout(tick, busy() || jobs().some(running) ? 1000 : 10_000);
  };
  tick();
  onCleanup(() => { stop = true; clearTimeout(timer); });
  return () => { clearTimeout(timer); tick(); }; // poll now
}

const mb = (n: number) => (n / 1048576).toFixed(n < 10 * 1048576 ? 1 : 0);
const steps = ["download", "check", "unpack", "deploy", "health"];

function label(j: StoreJob, step: string) {
  switch (step) {
    case "download":
      return j.total ? `Downloading ${mb(j.bytes ?? 0)} of ${mb(j.total)} MB` : j.bytes ? `Downloading ${mb(j.bytes)} MB` : "Downloading";
    case "check": return "Checking it against the catalog's hash";
    case "unpack": return "Unpacking";
    case "deploy": return j.action === "install" ? `Installing as ${j.app}` : `Switching to ${j.to}`;
    case "health": return "Waiting for it to come up";
    case "rollback": return `It didn't come up: rolling back to ${j.from}`;
  }
  return step;
}

// The steps with where the job is, a bar while it downloads, and how it ended.
export function JobProgress(props: { job?: StoreJob }) {
  const list = () => (props.job?.step === "rollback" ? [...steps, "rollback"] : steps);
  const at = () => list().indexOf(props.job?.step ?? "download");
  const state = (i: number) => {
    const j = props.job;
    if (!j) return i === 0 ? "now" : "todo";
    if (j.done && !j.error) return "done";
    if (i < at()) return "done";
    if (i === at()) return j.done ? "failed" : "now";
    return "todo";
  };
  const pct = () => (props.job?.total ? Math.min(100, Math.round(((props.job.bytes ?? 0) / props.job.total) * 100)) : 0);
  return (
    <div class="stack">
      <ol class="job-steps" aria-live="polite">
        <For each={list()}>
          {(s, i) => (
            <li class={`job-step job-${state(i())}`}>
              <span class="job-mark" aria-hidden="true">{{ done: "✓", now: "", failed: "✕", todo: "" }[state(i())]}</span>
              <span class="job-text">
                <span>{props.job ? label(props.job, s) : s === "download" ? "Starting" : label({ action: "install" } as StoreJob, s)}</span>
                <Show when={s === "download" && state(i()) === "now" && props.job?.total}>
                  <span class="meter" role="progressbar" aria-valuenow={pct()} aria-valuemin={0} aria-valuemax={100}><span style={{ width: `${pct()}%` }} /></span>
                </Show>
              </span>
            </li>
          )}
        </For>
      </ol>
      <Show when={props.job?.done && props.job.error}>
        <div class="note note-bad"><span>{props.job!.error}</span></div>
      </Show>
    </div>
  );
}

// A one-line status for a list row: "Updating · 45%", "Installing…", "Rolling back…".
export function jobLine(j: StoreJob) {
  if (j.step === "rollback") return "Rolling back…";
  const verb = j.action === "install" ? "Installing" : "Updating";
  if (j.step === "download" && j.total) return `${verb} · ${Math.round(((j.bytes ?? 0) / j.total) * 100)}%`;
  return `${verb}…`;
}
