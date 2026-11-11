import { render } from "solid-js/web";
import { Router, Route, Navigate } from "@solidjs/router";
import { Show, onMount, type ParentProps } from "solid-js";
import "@sparkstone/css";
import "./tokens.css";
import "./app.css";
import { api, user, setUser } from "./api";
import { Nav, Notices, ResultSheet, UpdateBanner, UpdateFooter, useUpdate } from "./components";
import Login from "./pages/Login";
import Invite from "./pages/Invite";
import Apps from "./pages/Apps";
import Logs from "./pages/Logs";
import Domains from "./pages/Domains";
import Services from "./pages/Services";
import Store from "./pages/Store";
import Users from "./pages/Users";
import Env from "./pages/Env";
import Deploy from "./pages/Deploy";

function Shell(props: ParentProps) {
  onMount(() => api<{ user: string | null }>("/api/session").then((s) => setUser(s.user)).catch(() => setUser(null)));
  return <>{props.children}<ResultSheet /><Notices /></>;
}

// pages that need a login
function Authed(props: ParentProps) {
  return (
    <Show when={user() !== undefined}>
      <Show when={user()} fallback={<Navigate href="/login" />}>
        <Nav />
        <Page>{props.children}</Page>
      </Show>
    </Show>
  );
}

function Page(props: ParentProps) {
  const { s, mutate } = useUpdate();
  return (
    <div class="dock-space">
      <main class="container page"><UpdateBanner s={s} />{props.children}</main>
      <UpdateFooter s={s} mutate={mutate} />
    </div>
  );
}

render(() => (
  <Router root={Shell}>
    <Route path="/login" component={Login} />
    <Route path="/invite" component={Invite} />
    <Route path="/" component={Authed}>
      <Route path="/" component={Apps} />
      <Route path="/apps/:app/sites/:site/logs" component={Logs} />
      <Route path="/apps/:app/sync-log" component={Logs} />
      <Route path="/apps/:app/env" component={Env} />
      <Route path="/apps/:app/deploy" component={Deploy} />
      <Route path="/domains" component={Domains} />
      <Route path="/services" component={Services} />
      <Route path="/store" component={Store} />
      <Route path="/store/install" component={Store} />
      <Route path="/users" component={Users} />
    </Route>
  </Router>
), document.getElementById("root")!);
