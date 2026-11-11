import { Show, createResource, createEffect } from "solid-js";
import { useParams } from "@solidjs/router";
import { api } from "../api";
import { PageHead } from "../components";

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
      <PageHead title={isSite() ? "Logs" : "Sync log"} back={{ href: "/", label: "Apps" }} detail={isSite() ? data()?.host || params.site : params.app}>
        <button class="btn btn-secondary btn-sm" onClick={() => refetch()}>Refresh</button>
      </PageHead>
      <Show when={data.error}><div class="alert alert-error">{String(data.error!.message)}</div></Show>
      <Show when={isSite() && data()?.status}><pre class="output">{data()!.status}</pre></Show>
      <pre ref={pre} class="output log">{data()?.log ?? "Loading…"}</pre>
    </div>
  );
}
