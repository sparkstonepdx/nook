import { Show, onMount } from "solid-js";
import { api, user } from "../api";
import { Logo } from "../components";
import { directory, loadDirectory } from "../directory";
import Launcher from "./Launcher";
import { PasswordSection } from "./Users";

// What a member sees: no dashboard, just the sites of the apps they were given, their password and log out.
export default function MemberHome() {
  onMount(loadDirectory);
  const logout = async () => { await api("/api/logout", {}).catch(() => {}); location.replace("/login"); };
  return (
    <main class="container page member-home">
      <header class="member-head">
        <span class="brand"><Logo /></span>
        <span class="member-who">{user()} <button class="btn btn-ghost btn-sm" onClick={logout}>Log out</button></span>
      </header>
      <section class="section">
        <h1>Your apps</h1>
        <Show when={directory()} fallback={<p class="empty">Loading…</p>}>
          <Show when={directory()!.length} fallback={<p class="empty">No apps yet. Ask an admin to add one to your account.</p>}>
            <Launcher />
          </Show>
        </Show>
      </section>
      <PasswordSection />
    </main>
  );
}
