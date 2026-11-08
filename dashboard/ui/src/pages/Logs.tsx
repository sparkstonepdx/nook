import { Show, createResource, createEffect } from "solid-js";
import { A, useParams } from "@solidjs/router";
import { api } from "../api";

export default function Logs() {
  const params = useParams();
  const isSite = () => !!params.site;
  const [data, { refetch }] = createResource(
    () => ({ app: params.app, site: params.site }),
    (p) => p.site
      ? api<{ host: string; status: string; log: string }>(`/api/apps/${p.app}/sites/${encodeURIComponent(p.site)}/logs`)
      : api<{ log: string }>(`/api/apps/${p.app}/sync-log`).then((r) => ({ host: "", status: "", ...r })),
  );
  let pre!: HTMLPreElement;
  createEffect(() => { if (data()) queueMicrotask(() => (pre.scrollTop = pre.scrollHeight)); });
  return (
    <div class="stack">
      <div class="page-head">
        <A class="btn btn-ghost btn-sm" href="/">← Apps</A>
        <h1>{isSite() ? data()?.host || params.site : `${params.app}: sync log`}</h1>
        <span class="spacer" />
        <button class="btn btn-secondary" onClick={() => refetch()}>Refresh</button>
      </div>
      <Show when={data.error}><div class="alert alert-error">{String(data.error.message)}</div></Show>
      <Show when={isSite() && data()?.status}><pre class="output mono card-border">{data()!.status}</pre></Show>
      <pre ref={pre} class="output log mono card-border">{data()?.log ?? "loading…"}</pre>
    </div>
  );
}
