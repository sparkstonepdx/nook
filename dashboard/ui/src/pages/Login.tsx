import { createSignal } from "solid-js";
import { useNavigate, useSearchParams } from "@solidjs/router";
import { api, setUser } from "../api";
import { ErrorAlert, Field } from "../components";

export default function Login() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  // back to where the login interrupted: a path on this dashboard only
  const next = () => { const n = String(params.next || ""); return n.startsWith("/") && !n.startsWith("//") ? n : "/"; };
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget as HTMLFormElement);
    setBusy(true); setError("");
    try {
      const r = await api<{ user: string }>("/api/login", { user: f.get("user"), password: f.get("password") });
      setUser(r.user); navigate(next(), { replace: true });
    } catch (err) { setError((err as Error).message); } finally { setBusy(false); }
  };
  return (
    <main class="auth">
      <p class="brand">nook</p>
      <div class="card">
      <h1>Log in</h1>
      <form onSubmit={submit}>
        <ErrorAlert error={error()} />
        <Field label="Username"><input class="input" name="user" autocomplete="username" required autofocus /></Field>
        <Field label="Password"><input class="input" name="password" type="password" autocomplete="current-password" required /></Field>
        <button class="btn" disabled={busy()}>Log in</button>
      </form>
      </div>
    </main>
  );
}
