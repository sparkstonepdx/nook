import { For, Show, createResource, createRoot, createSignal, onCleanup } from "solid-js";
import { api, attempt, runCmd, say } from "./api";
import { Confirm, Field, Sheet } from "./components";

// Backups (core/backup.go): set up once with an S3-compatible bucket, then daily, before each update, and on
// demand. Its own page (/backups); each app's Manage sheet has its own switch and Restore.
type AppBackup = { app: string; snapshot?: string; added: number; size: number; files: number; frozen?: boolean; hooks?: number; error?: string };
export type BackupStatus = {
  configured: boolean; repo?: string; serverId: string; time: string; keep: string; skip: string[];
  running: boolean; started?: number; finished?: number; reason?: string; ok: boolean; error?: string;
  server?: AppBackup; apps: AppBackup[]; checked?: number; checkError?: string; lastOk?: number;
};
type Snapshot = { id: string; short_id: string; time: string; hostname: string; tags: string[] };

// fetched when a page that shows it opens, not at startup (which may be the login page)
const [on, setOn] = createSignal(false);
const [status, { refetch: refetchStatus }] = createRoot(() => createResource(on, () => api<BackupStatus>("/api/backup").catch(() => undefined)));
const load = () => (on() ? refetchStatus() : setOn(true));
export { status as backupStatus, refetchStatus };

const size = (b: number) => (b >= 1 << 30 ? `${(b / (1 << 30)).toFixed(1)} GB` : b >= 1 << 20 ? `${Math.round(b / (1 << 20))} MB` : b >= 1024 ? `${Math.round(b / 1024)} KB` : `${b} B`);
const when = (unix: number) => new Date(unix * 1000).toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" });

export function Backups() {
  load();
  // poll while one runs
  const timer = setInterval(() => status()?.running && refetchStatus(), 2000);
  onCleanup(() => clearInterval(timer));
  return (
    <div class="stack backups">
      <Show when={status()} fallback={<p class="hint">Reading…</p>}>
        {(s) => <Show when={s().configured} fallback={<BackupSetup />}><BackupState s={s()} /></Show>}
      </Show>
    </div>
  );
}

function BackupState(props: { s: BackupStatus }) {
  const s = () => props.s;
  const added = () => s().apps.reduce((n, a) => n + a.added, s().server?.added ?? 0);
  const run = async () => {
    await attempt("Couldn't start a backup", async () => { await api("/api/backup/run", {}); say("Backup started", { kind: "success" }); });
    refetchStatus();
  };
  return (
    <div class="stack">
      <Show when={s().running} fallback={
        <Show when={s().finished} fallback={<p>Set up. The first backup runs at {s().time}, or now.</p>}>
          <Show when={s().ok} fallback={<div class="note note-bad"><span>The last backup failed ({when(s().finished!)}): {s().error}</span></div>}>
            <p>Last backup {when(s().finished!)}: {s().apps.length + 1} snapshots, {size(added())} new. Next at {s().time}.</p>
          </Show>
        </Show>
      }>
        <p class="note">Backing up… {s().apps.length} {s().apps.length === 1 ? "app" : "apps"} done so far.</p>
      </Show>
      <Show when={s().checkError}><div class="note note-bad"><span>The weekly check found a problem: {s().checkError}</span></div></Show>
      <p class="hint">To <span class="mono">{s().repo}</span>, encrypted. Kept: {s().keep.split(",").map((n, i) => `${n} ${["daily", "weekly", "monthly"][i]}`).join(", ")}. This server is <span class="mono">{s().serverId}</span>.<Show when={s().skip.length}> Left out: {s().skip.join(", ")}.</Show></p>
      <div class="actions-row"><button class="btn btn-secondary btn-sm" disabled={s().running} onClick={run}>Back up now</button></div>
    </div>
  );
}

function BackupSetup() {
  const [existing, setExisting] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [password, setPassword] = createSignal("");
  const [error, setError] = createSignal("");
  return (
    <Show when={!password()} fallback={
      <div class="stack">
        <div class="note note-warn"><span>Backups are set up. This is their password, shown once: keep it somewhere other than this server, like a password manager. Without it nobody can read the backups, you included.</span></div>
        <div class="field-row">
          <input class="input mono" readonly value={password()} onFocus={(e) => e.currentTarget.select()} />
          <button type="button" class="btn btn-secondary" onClick={() => { navigator.clipboard?.writeText(password()); say("Password copied"); }}>Copy</button>
        </div>
        <button class="btn" onClick={() => { setPassword(""); refetchStatus(); }}>I've saved it</button>
      </div>
    }>
      <form class="stack" onSubmit={async (e) => {
        e.preventDefault();
        const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>;
        setBusy(true); setError("");
        try {
          const r = await api<{ password: string }>("/api/backup/setup", { repo: f.repo, key: f.key, time: f.time, set: { secret: f.secret, password: existing() ? f.password : "" } });
          if (r.password) setPassword(r.password); else { say("Backups are set up", { kind: "success" }); refetchStatus(); }
        } catch (err) { setError((err as Error).message); }
        setBusy(false);
      }}>
        <p class="hint">Daily, before each nook update, and when you ask: nook's settings, users and certificates, and each app's folder, encrypted, to an S3-compatible bucket (Cloudflare R2, Backblaze B2, any S3).</p>
        <Field label="Destination" hint="s3:https://ENDPOINT/BUCKET/PATH. For R2: s3:https://ACCOUNT_ID.r2.cloudflarestorage.com/BUCKET/nook">
          <input class="input mono" name="repo" required placeholder="s3:https://…r2.cloudflarestorage.com/backups/nook" autocomplete="off" />
        </Field>
        <div class="field-row">
          <Field label="Access key id"><input class="input mono" name="key" required autocomplete="off" /></Field>
          <Field label="Secret access key"><input class="input mono" name="secret" type="password" required autocomplete="off" /></Field>
        </div>
        <Field label="Daily at" hint="Server time"><input class="input" name="time" type="time" value="03:17" required /></Field>
        <label class="switch-row">
          <input type="checkbox" role="switch" checked={existing()} onChange={(e) => setExisting(e.currentTarget.checked)} />
          <span><span>Use an existing backup repository</span><small>Another server's, or this one's from before a reinstall. Needs its password.</small></span>
        </label>
        <Show when={existing()}><Field label="Its password"><input class="input mono" name="password" type="password" required autocomplete="off" /></Field></Show>
        <Show when={error()}><div class="note note-bad"><span>{error()}</span></div></Show>
        <div class="actions-row"><button class="btn" disabled={busy()}>{busy() ? "Setting up…" : "Set up backups"}</button></div>
      </form>
    </Show>
  );
}

// In an app's Manage sheet: whether it's backed up, its last snapshot, and Restore.
export function AppBackupRow(props: { app: string; after: (p?: Promise<unknown>) => void }) {
  if (!on()) setOn(true);
  const [open, setOpen] = createSignal(false);
  const s = () => status();
  const skipped = () => !!s()?.skip.includes(props.app);
  const last = () => s()?.apps.find((a) => a.app === props.app);
  return (
    <Show when={s()?.configured}>
      <label class="switch-row">
        <input type="checkbox" role="switch" checked={!skipped()} onChange={async (e) => {
          const on = e.currentTarget.checked;
          await runCmd(on ? `${props.app} is backed up` : `${props.app} is left out of backups`, `/api/apps/${props.app}/backup-skip`, { skip: !on });
          refetchStatus();
        }} />
        <span><span>Back up</span><small>
          <Show when={!skipped()} fallback="Left out: its data isn't in the backups.">
            <Show when={last()?.snapshot} fallback="In the next backup.">Last backup {when(s()!.finished ?? 0)}, {size(last()!.size)}{last()!.frozen ? ", paused while read" : ""}{last()!.hooks ? ", after its backup hook" : ""}.</Show>
          </Show>
          {" "}A site's nook.json can leave folders out: {`{"backup": {"skip": ["data/cache"]}}`}.
        </small></span>
      </label>
      <button class="btn btn-secondary btn-sm restore-open" onClick={() => setOpen(true)}>Restore from a backup…</button>
      <RestoreSheet app={props.app} open={open()} onClose={() => setOpen(false)} after={props.after} />
    </Show>
  );
}

function RestoreSheet(props: { app: string; open: boolean; onClose: () => void; after: (p?: Promise<unknown>) => void }) {
  const [snaps] = createResource(() => props.open && props.app, (app) => api<{ snapshots: Snapshot[] }>(`/api/backup/snapshots?app=${encodeURIComponent(app)}`).then((r) => r.snapshots ?? []));
  return (
    <Sheet open={props.open} title={`Restore ${props.app}`} onClose={props.onClose}>
      <div class="stack">
        <p class="hint">Puts {props.app}'s data folder back as it was in a snapshot and restarts it. The data there now is kept beside it as <span class="mono">data.before-restore-…</span>, so a restore can be undone by hand.</p>
        <Show when={!snaps.error} fallback={<div class="note note-bad"><span>{String(snaps.error?.message)}</span></div>}>
          <Show when={snaps()} fallback={<p class="hint">Reading the snapshots…</p>}>
            <ul class="list">
              <For each={snaps()} fallback={<li class="row"><span class="empty">No snapshots of {props.app} yet.</span></li>}>
                {(sn) => (
                  <li class="row">
                    <span class="row-main"><span>{new Date(sn.time).toLocaleString([], { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}</span><small class="mono">{sn.short_id}</small></span>
                    <span class="row-end">
                      <Confirm label="Restore" class="btn btn-secondary btn-sm" ask={`Replace ${props.app}'s data with this?`} yes="Restore" onYes={() => { props.onClose(); props.after(runCmd(`Restored ${props.app}`, `/api/apps/${props.app}/restore`, { snapshot: sn.id })); }} />
                    </span>
                  </li>
                )}
              </For>
            </ul>
          </Show>
        </Show>
      </div>
    </Sheet>
  );
}
