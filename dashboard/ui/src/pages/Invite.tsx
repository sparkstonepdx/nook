import { Show, createResource, createSignal } from "solid-js";
import { useNavigate, useSearchParams } from "@solidjs/router";
import { api, setUser } from "../api";
import { ErrorAlert, Field, Logo } from "../components";

export default function Invite() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const token = () => String(params.t || "");
  const [check] = createResource(token, (t) => api<{ valid: boolean }>(`/api/invite?t=${encodeURIComponent(t)}`));
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget as HTMLFormElement);
    setBusy(true); setError("");
    try {
      const r = await api<{ user: string }>("/api/invite", { token: token(), user: f.get("user"), password: f.get("password"), password2: f.get("password2") });
      setUser(r.user); navigate("/", { replace: true });
    } catch (err) { setError((err as Error).message); } finally { setBusy(false); }
  };
  return (
    <main class="auth">
      <p class="brand"><Logo /></p>
      <div class="card">
      <h1>Create your login</h1>
      <Show when={!check.loading}>
        <Show when={check()?.valid} fallback={<p>This invite was already used or has expired. Ask an admin for a new one, or run <code>app invite</code> on the server.</p>}>
          <form onSubmit={submit}>
            <ErrorAlert error={error()} />
            <Field label="Username" hint="2-32 of a-z 0-9 . _ -">
              <input class="input" name="user" autocomplete="username" required pattern="[a-z0-9._\-]{2,32}" autofocus />
            </Field>
            <Field label="Password" hint="12+ characters">
              <input class="input" name="password" type="password" autocomplete="new-password" required minlength={12} />
            </Field>
            <Field label="Password again">
              <input class="input" name="password2" type="password" autocomplete="new-password" required minlength={12} />
            </Field>
            <button class="btn" disabled={busy()}>Create login</button>
          </form>
        </Show>
      </Show>
      </div>
    </main>
  );
}
