import { For, Show, createResource, createSignal } from "solid-js";
import { api, attempt, say } from "../api";
import { Field, PageHead, Confirm, Sheet } from "../components";

type User = { name: string; created: string; invitedBy: string; me: boolean; admin: boolean; apps: string[] };
type Users = { users: User[]; inviteHours: number };

// Admin: the whole dashboard and every app. Member: no dashboard, only the sites of the apps ticked here, when
// those apps have their nook login on.
function Access(props: { apps: string[]; admin: boolean; picked: string[]; onChange: (admin: boolean, picked: string[]) => void }) {
  return (
    <div class="stack">
      <div class="seg" role="group" aria-label="Access">
        <button type="button" classList={{ on: props.admin }} aria-pressed={props.admin} onClick={() => props.onChange(true, props.picked)}>Admin</button>
        <button type="button" classList={{ on: !props.admin }} aria-pressed={!props.admin} onClick={() => props.onChange(false, props.picked)}>Only some apps</button>
      </div>
      <Show when={!props.admin}>
        <Show when={props.apps.length} fallback={<p class="hint">No apps yet.</p>}>
          <div class="app-picks">
            <For each={props.apps}>
              {(a) => (
                <label class="pick"><input type="checkbox" checked={props.picked.includes(a)}
                  onChange={(e) => props.onChange(false, e.currentTarget.checked ? [...props.picked, a] : props.picked.filter((x) => x !== a))} /> {a}</label>
              )}
            </For>
          </div>
        </Show>
        <p class="hint">They see these apps' sites and nothing of the dashboard. An app's sites only ask for a login when its Nook login switch is on (Apps, Manage).</p>
      </Show>
    </div>
  );
}

export default function UsersPage() {
  const [data, { refetch }] = createResource(() => api<Users>("/api/users"));
  const [apps] = createResource(() => api<{ apps: { name: string }[] }>("/api/overview").then((o) => o.apps.map((a) => a.name)).catch(() => [] as string[]));
  const [link, setLink] = createSignal("");
  const [inv, setInv] = createSignal<{ admin: boolean; picked: string[] }>({ admin: true, picked: [] });
  const [editing, setEditing] = createSignal<{ name: string; admin: boolean; picked: string[] } | null>(null);
  const act = async (fn: () => Promise<unknown>, ok: string) => {
    if (await attempt("That didn't work", fn)) { say(ok, { kind: "success" }); refetch(); }
  };
  return (
    <>
      <PageHead title="Users" />
      <section class="section">
        <ul class="list">
          <For each={data()?.users}>
            {(u) => (
              <li class="row">
                <span class="row-main"><span>{u.name}<Show when={u.me}> <span class="badge">you</span></Show> <span class="badge">{u.admin ? "admin" : u.apps.length ? u.apps.join(", ") : "no apps"}</span></span><small>since {u.created.slice(0, 10)} · invited by {u.invitedBy || "the setup link"}</small></span>
                <span class="row-end"><Show when={!u.me}>
                  <button class="btn btn-ghost btn-sm" onClick={() => setEditing({ name: u.name, admin: u.admin, picked: u.apps })}>Access</button>
                  <Confirm label="Remove" yes="Remove" ask={`Remove ${u.name}? They're logged out right away, apps included.`} onYes={() => act(() => api(`/api/users/${encodeURIComponent(u.name)}/remove`, {}), `Removed ${u.name}`)} />
                </Show></span>
              </li>
            )}
          </For>
        </ul>
      </section>

      <section class="section">
        <h2>Invite someone</h2>
        <div class="card stack">
          <p class="hint">A one-time link, good for {data()?.inviteHours ?? 24} hours. Whoever opens it picks a name and password.</p>
          <Show when={!link()}>
            <Access apps={apps() ?? []} admin={inv().admin} picked={inv().picked} onChange={(admin, picked) => setInv({ admin, picked })} />
          </Show>
          <Show when={link()} fallback={<button class="btn" style={{ "align-self": "flex-start" }} disabled={!inv().admin && !inv().picked.length} onClick={() => act(async () => setLink((await api<{ link: string }>("/api/users/invite", { admin: inv().admin, apps: inv().picked })).link), "Invite link made. Send it privately.")}>Make an invite link</button>}>
            <div class="field-row">
              <input class="input mono" readonly value={link()} aria-label="Invite link" onFocus={(e) => e.currentTarget.setSelectionRange(0, e.currentTarget.value.length)} />
              <button class="btn btn-secondary" onClick={() => { navigator.clipboard?.writeText(link()); say("Invite link copied"); }}>Copy</button>
            </div>
            <button class="btn btn-ghost btn-sm" style={{ "align-self": "flex-start" }} onClick={() => { setLink(""); setInv({ admin: true, picked: [] }); }}>Make another</button>
          </Show>
        </div>
      </section>

      <PasswordSection />

      <Sheet open={!!editing()} title={editing() ? `${editing()!.name}'s access` : ""} onClose={() => setEditing(null)}
        footer={<><button class="btn btn-secondary" onClick={() => setEditing(null)}>Cancel</button><button class="btn" onClick={() => {
          const e = editing()!; setEditing(null);
          act(() => api(`/api/users/${encodeURIComponent(e.name)}/access`, { admin: e.admin, apps: e.picked }), `Changed ${e.name}'s access`);
        }}>Save</button></>}>
        <Show when={editing()}>
          {(e) => <Access apps={apps() ?? []} admin={e().admin} picked={e().picked} onChange={(admin, picked) => setEditing({ ...e(), admin, picked })} />}
        </Show>
      </Sheet>
    </>
  );
}

export function PasswordSection() {
  return (
    <section class="section">
      <h2>Your password</h2>
      <form class="card stack" onSubmit={async (e) => {
        e.preventDefault();
        const form = e.currentTarget;
        const f = Object.fromEntries(new FormData(form));
        if (await attempt("That didn't work", () => api("/api/password", f).then(() => form.reset()))) say("Password changed", { kind: "success" });
      }}>
        <div class="field-row">
          <Field label="New password"><input class="input" name="password" type="password" autocomplete="new-password" minlength={12} required /></Field>
          <Field label="Again"><input class="input" name="password2" type="password" autocomplete="new-password" minlength={12} required /></Field>
          <button class="btn btn-secondary">Change</button>
        </div>
        <p class="hint">At least 12 characters. Your other sessions are logged out, apps included.</p>
      </form>
    </section>
  );
}
