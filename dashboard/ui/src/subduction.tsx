import { Show, createResource } from "solid-js";
import { api, say } from "./api";
import { Field } from "./components";

type SubductionId = { installed: boolean; url?: string; peerId?: string; peerIdHex?: string; card?: string; problem?: string };

// What an app needs to reach this server's Subduction relay with Keyhive: its address, its peer id and its
// contact card, each with its own Copy. In the Manage sheet of a Subduction app (the catalog's, whatever its name
// and site, or the hand setup's); nothing for any other app.
export function SubductionIdentity(props: { app: string }) {
  const [id] = createResource(() => props.app, (app) => api<SubductionId>(`/api/subduction?app=${encodeURIComponent(app)}`).catch(() => undefined));
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
