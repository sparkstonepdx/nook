import { Show, createResource, createSignal, onCleanup } from "solid-js";
import { api, runCmd, say } from "./api";
import { Field } from "./components";

// One-off: moving the Subduction app set up by hand onto the catalog's Subduction (core/subductionmove.go).
// Remove with it once the move is done.
type Move = { state: "" | "moving" | "testing" | "failed" | "reverted" | "done"; step?: string; error?: string; out?: string; newApp?: string; offer: boolean };

export function SubductionMoveCard(props: { after: (p?: Promise<unknown>) => void }) {
  const [m, { refetch }] = createResource(() => api<Move>("/api/subduction/move").catch(() => undefined));
  const timer = setInterval(() => { if (!document.hidden && m()?.state === "moving") refetch(); }, 2000);
  onCleanup(() => clearInterval(timer));
  const [confirm, setConfirm] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const start = async () => {
    setBusy(true);
    try { await api("/api/subduction/move", {}); } catch (e) { say("The move didn't start", { kind: "error", detail: (e as Error).message }); }
    setBusy(false);
    refetch();
  };
  const finish = (keep: boolean) => {
    setConfirm("");
    props.after(runCmd(keep ? "Deleted the old Subduction app" : "Back on the old Subduction app", "/api/subduction/move/finish", { keep }).finally(refetch));
  };
  const show = () => { const x = m(); return !!x && (x.offer || x.state === "moving" || x.state === "testing" || x.state === "failed"); };
  return (
    <Show when={show() && m()}>
      {(x) => (
        <section class="card-border move-card">
          <h2>Move Subduction to the catalog</h2>
          <Show when={x().state === "" || x().state === "reverted" || (x().state === "failed" && x().offer)}>
            <Show when={x().state === "failed"}><div class="note note-bad"><span>{x().error} The old server is back.</span></div></Show>
            <p class="hint">Subduction here was set up by hand. This installs the catalog's Subduction on the same address with the same key and documents, so apps see the same server. It stops the old one, copies its data and starts the new one; if anything fails the old one comes back. Then you test, and keep it or go back.</p>
            <button class="btn" disabled={busy()} onClick={start}>Move it</button>
          </Show>
          <Show when={x().state === "moving"}>
            <p class="note"><span>{x().step}…</span></p>
            <p class="hint">Apps can't sync for the minute this takes.</p>
          </Show>
          <Show when={x().state === "failed" && !x().offer}>
            <div class="note note-bad"><span>{x().error}</span></div>
            <button class="btn btn-secondary" onClick={() => finish(false)}>Go back to the old server</button>
          </Show>
          <Show when={x().state === "testing"}>
            <p class="note"><span>The catalog's Subduction ({x().newApp}) is serving subduction with the old server's key and documents. The old app is stopped, not deleted.</span></p>
            <p class="hint">Open your apps and check they sync, on a device that has been offline too.</p>
            <button class="btn btn-secondary" onClick={() => finish(false)}>Go back to the old server</button>
            <form class="danger-zone" onSubmit={(e) => { e.preventDefault(); finish(true); }}>
              <Field label="Keep the new one" hint="Deletes the old app subduction and its copy of the data. Type subduction to confirm.">
                <input class="input" value={confirm()} onInput={(e) => setConfirm(e.currentTarget.value)} autocomplete="off" placeholder="subduction" />
              </Field>
              <button class="btn danger-fill" disabled={confirm() !== "subduction"}>Keep it and delete the old app</button>
            </form>
          </Show>
        </section>
      )}
    </Show>
  );
}
