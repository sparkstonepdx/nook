import { Show, createResource, createEffect, createMemo, createSignal } from "solid-js";
import { useParams } from "@solidjs/router";
import { api, say } from "../api";
import { PageHead } from "../components";
import { Ansi } from "../ansi";
import { LogView, parseLine, plain } from "../logview";

const RAW_KEY = "nook:logs-raw";
const LEVELS = { all: "All", warn: "Warnings", error: "Errors" } as const;
type Filter = keyof typeof LEVELS;
const keep: Record<Filter, (l?: string) => boolean> = {
  all: () => true,
  warn: (l) => l === "WARN" || l === "ERROR",
  error: (l) => l === "ERROR",
};

export default function Logs() {
  const params = useParams();
  const isSite = () => !!params.site;
  const [data, { refetch }] = createResource(
    () => ({ app: params.app, site: params.site }),
    (p) => p.site
      ? api<{ host: string; status: string; log: string }>(`/api/apps/${p.app}/sites/${encodeURIComponent(p.site)}/logs`)
      : api<{ log: string }>(`/api/apps/${p.app}/sync-log`).then((r) => ({ host: "", status: "", ...r })),
  );
  const [raw, setRawSignal] = createSignal((() => { try { return localStorage.getItem(RAW_KEY) === "1"; } catch { return false; } })());
  const setRaw = (on: boolean) => { setRawSignal(on); try { localStorage.setItem(RAW_KEY, on ? "1" : "0"); } catch { /* storage off */ } };
  const [filter, setFilter] = createSignal<Filter>("all");

  const lines = createMemo(() => (data()?.log ?? "").replace(/\n+$/, "").split("\n").map(parseLine));
  // anything with levels can be narrowed to its warnings and errors; a line with no level of its own (a panic's
  // backtrace, say) goes with the line before it
  const leveled = createMemo(() => lines().some((l) => l.level));
  const shown = createMemo(() => {
    if (filter() === "all") return lines();
    let last: string | undefined;
    return lines().filter((l) => { if (l.level) last = l.level; return keep[filter()](last); });
  });
  const copy = () => {
    const text = raw() && filter() === "all" ? plain(data()?.log ?? "") : shown().map((l) => l.plain).join("\n");
    navigator.clipboard?.writeText(text).then(() => say(`${shown().length} lines copied`), () => say("Couldn't copy", { kind: "error" }));
  };

  let box!: HTMLElement;
  createEffect(() => { shown(); raw(); if (data()) queueMicrotask(() => box && (box.scrollTop = box.scrollHeight)); });
  return (
    <div class="stack">
      <PageHead title={isSite() ? "Logs" : "Sync log"} back={{ href: "/", label: "Apps" }} detail={isSite() ? data()?.host || params.site : params.app}>
        <button class="btn btn-secondary btn-sm" disabled={!data()} onClick={copy}>Copy</button>
        <button class="btn btn-secondary btn-sm" onClick={() => refetch()}>Refresh</button>
      </PageHead>
      <Show when={data.error}><div class="note note-bad">{String(data.error!.message)}</div></Show>
      <Show when={isSite() && data()?.status}><pre class="output"><Ansi text={data()!.status} /></pre></Show>
      <div class="lv-bar">
        <Show when={leveled()}>
          <div class="seg" role="group" aria-label="Show">
            {(Object.keys(LEVELS) as Filter[]).map((f) => <button classList={{ on: filter() === f }} onClick={() => setFilter(f)}>{LEVELS[f]}</button>)}
          </div>
        </Show>
        <label class="lv-raw"><input type="checkbox" checked={raw()} onChange={(e) => setRaw(e.currentTarget.checked)} /> Raw</label>
      </div>
      <Show when={data()} fallback={<pre class="output log">Loading…</pre>}>
        <Show
          when={!raw()}
          fallback={<pre ref={(el) => (box = el)} class="output log"><Ansi text={filter() === "all" ? data()!.log : shown().map((l) => l.raw).join("\n")} /></pre>}
        >
          <div ref={(el) => (box = el)} class="output log lv-box"><LogView lines={shown()} /></div>
        </Show>
      </Show>
    </div>
  );
}
