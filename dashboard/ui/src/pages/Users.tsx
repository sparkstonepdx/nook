import { For, Show, createResource, createSignal } from "solid-js";
import { api, say } from "../api";
import { Field, PageHead, Confirm } from "../components";

type Users = { users: { name: string; created: string; invitedBy: string; me: boolean }[]; inviteHours: number };

export default function UsersPage() {
  const [data, { refetch }] = createResource(() => api<Users>("/api/users"));
  const [link, setLink] = createSignal("");
  const act = async (fn: () => Promise<unknown>, ok: string) => {
    try { await fn(); say(ok, { kind: "success" }); refetch(); } catch (e) { say("That didn't work", { kind: "error", detail: (e as Error).message }); }
  };
  return (
    <>
      <PageHead title="Users" />
      <section class="section">
        <ul class="list">
          <For each={data()?.users}>
            {(u) => (
              <li class="row">
                <span class="row-main"><span>{u.name}<Show when={u.me}> <span class="badge">you</span></Show></span><small>since {u.created.slice(0, 10)} · invited by {u.invitedBy || "the setup link"}</small></span>
                <span class="row-end"><Show when={!u.me}>
                  <Confirm label="Remove" yes="Remove" ask={`Remove ${u.name}? They're logged out right away.`} onYes={() => act(() => api(`/api/users/${encodeURIComponent(u.name)}/remove`, {}), `Removed ${u.name}`)} />
                </Show></span>
              </li>
            )}
          </For>
        </ul>
      </section>

      <section class="section">
        <h2>Invite someone</h2>
        <div class="card stack">
          <p class="hint">A one-time link, good for {data()?.inviteHours ?? 24} hours. Whoever opens it picks a name and password and gets full access.</p>
          <Show when={link()} fallback={<button class="btn" style={{ "align-self": "flex-start" }} onClick={() => act(async () => setLink((await api<{ link: string }>("/api/users/invite", {})).link), "Invite link made. Send it privately.")}>Make an invite link</button>}>
            <div class="field-row">
              <input class="input mono" readonly value={link()} aria-label="Invite link" onFocus={(e) => e.currentTarget.setSelectionRange(0, e.currentTarget.value.length)} />
              <button class="btn btn-secondary" onClick={() => { navigator.clipboard?.writeText(link()); say("Invite link copied"); }}>Copy</button>
            </div>
          </Show>
        </div>
      </section>

      <section class="section">
        <h2>Your password</h2>
        <form class="card stack" onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const f = Object.fromEntries(new FormData(form));
          act(() => api("/api/password", f).then(() => form.reset()), "Password changed");
        }}>
          <div class="field-row">
            <Field label="New password"><input class="input" name="password" type="password" autocomplete="new-password" minlength={12} required /></Field>
            <Field label="Again"><input class="input" name="password2" type="password" autocomplete="new-password" minlength={12} required /></Field>
            <button class="btn btn-secondary">Change</button>
          </div>
          <p class="hint">At least 12 characters. Your other sessions are logged out.</p>
        </form>
      </section>
    </>
  );
}
