import { For, Show, createResource, createSignal } from "solid-js";
import { api } from "../api";
import { ErrorAlert, Field } from "../components";

type Users = { users: { name: string; created: string; invitedBy: string; me: boolean }[]; inviteHours: number };

export default function UsersPage() {
  const [data, { refetch }] = createResource(() => api<Users>("/api/users"));
  const [link, setLink] = createSignal("");
  const [msg, setMsg] = createSignal("");
  const [error, setError] = createSignal("");
  const act = async (fn: () => Promise<unknown>, ok: string) => {
    setError(""); setMsg("");
    try { await fn(); setMsg(ok); refetch(); } catch (e) { setError((e as Error).message); }
  };
  return (
    <div class="stack">
      <div class="page-head"><h1>Users</h1></div>
      <ErrorAlert error={error()} />
      <Show when={msg()}><div class="alert">{msg()}</div></Show>

      <section class="card">
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>User</th><th>Created</th><th>Invited by</th><th /></tr></thead>
            <tbody>
              <For each={data()?.users}>
                {(u) => (
                  <tr>
                    <td>{u.name} <Show when={u.me}><span class="badge">you</span></Show></td>
                    <td>{u.created.slice(0, 10)}</td>
                    <td class="text-secondary">{u.invitedBy || "setup link"}</td>
                    <td><div class="actions"><Show when={!u.me}>
                      <button class="btn btn-ghost btn-sm" onClick={() => window.confirm(`Remove ${u.name}? They are logged out immediately.`) && act(() => api(`/api/users/${encodeURIComponent(u.name)}/remove`, {}), `Removed ${u.name}.`)}>Remove</button>
                    </Show></div></td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </section>

      <section class="card">
        <h2>Invite someone</h2>
        <p class="text-secondary hint">A one-time link, valid {data()?.inviteHours ?? 24} hours. Whoever opens it picks a username and password and gets full access.</p>
        <div class="row" style={{ "margin-top": "0.75rem" }}>
          <button class="btn btn-primary" onClick={() => act(async () => setLink((await api<{ link: string }>("/api/users/invite", {})).link), "Invite link created. Send it privately.")}>Make invite link</button>
          <Show when={link()}>
            <input class="input mono" readonly value={link()} style={{ flex: "1 1 20rem" }} onFocus={(e) => e.currentTarget.select()} />
            <button class="btn btn-secondary" onClick={() => navigator.clipboard?.writeText(link())}>Copy</button>
          </Show>
        </div>
      </section>

      <section class="card">
        <h2>Change your password</h2>
        <p class="text-secondary hint">Logs out your other sessions.</p>
        <form class="form-row" style={{ "margin-top": "0.75rem" }} onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const f = Object.fromEntries(new FormData(form));
          act(() => api("/api/password", f).then(() => form.reset()), "Password changed.");
        }}>
          <Field label="New password"><input class="input" name="password" type="password" autocomplete="new-password" minlength={12} required /></Field>
          <Field label="Again"><input class="input" name="password2" type="password" autocomplete="new-password" minlength={12} required /></Field>
          <button class="btn btn-secondary">Change</button>
        </form>
      </section>
    </div>
  );
}
