// app-host dashboard: list apps and their sites, run the admin commands, read logs.
// Runs as root on 127.0.0.1 behind Caddy. Auth is HTTP basic auth with one long password.
//   DASHBOARD_PASSWORD=... DASHBOARD_PORT=19999 node server.mjs
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawn } from "node:child_process";

const CONFIG = Object.fromEntries(
  fs.readFileSync(process.env.APPS_CONFIG || "/etc/apps/config", "utf8")
    .split("\n").map((l) => l.match(/^([A-Z_]+)=("?)(.*)\2$/)).filter(Boolean).map((m) => [m[1], m[3]]),
);
const PASSWORD = process.env.DASHBOARD_PASSWORD || "";
const PORT = Number(process.env.DASHBOARD_PORT || 19999);
if (PASSWORD.length < 32) { console.error("DASHBOARD_PASSWORD must be at least 32 characters"); process.exit(1); }

const APPS_DIR = CONFIG.APPS_DIR || "/srv/apps";
const NAME_RE = /^[a-z][a-z0-9-]{1,23}$/;
const SITE_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

// ---------- helpers ----------
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

function run(cmd, args, { input, timeout = 120_000 } = {}) {
  return new Promise((resolve) => {
    const p = spawn(cmd, args, { stdio: ["pipe", "pipe", "pipe"] });
    let out = "";
    const t = setTimeout(() => { p.kill("SIGKILL"); out += "\n[timed out]"; }, timeout);
    p.stdout.on("data", (d) => (out += d));
    p.stderr.on("data", (d) => (out += d));
    p.on("error", (e) => { clearTimeout(t); resolve({ code: -1, out: out + String(e) }); });
    p.on("close", (code) => { clearTimeout(t); resolve({ code, out }); });
    p.stdin.end(input ?? "");
  });
}

async function unitProps(unit, props) {
  const { out } = await run("systemctl", ["show", unit, "-p", props.join(",")], { timeout: 5000 });
  return Object.fromEntries(out.split("\n").filter((l) => l.includes("=")).map((l) => [l.slice(0, l.indexOf("=")), l.slice(l.indexOf("=") + 1)]));
}

const mb = (v) => (/^\d+$/.test(v || "") ? `${(Number(v) / 1048576).toFixed(0)}M` : "");

function ports() {
  try {
    return fs.readFileSync(CONFIG.PORTS_FILE, "utf8").split("\n").filter(Boolean).map((l) => l.split(" "));
  } catch { return []; }
}

async function listApps() {
  let names = [];
  try { names = fs.readdirSync(APPS_DIR).filter((n) => NAME_RE.test(n)).sort(); } catch {}
  const portList = ports();
  return Promise.all(names.map(async (name) => {
    const sitesDir = path.join(APPS_DIR, name, "sites");
    let sites = [];
    try { sites = fs.readdirSync(sitesDir).filter((s) => !s.startsWith(".")).sort(); } catch {}
    const slice = await unitProps(`app-${name}.slice`, ["MemoryCurrent", "MemoryMax", "CPUQuotaPerSecUSec"]);
    const watcher = await unitProps(`app-sync@${name}.path`, ["ActiveState"]);
    return {
      name,
      memory: mb(slice.MemoryCurrent), memoryMax: mb(slice.MemoryMax), cpu: slice.CPUQuotaPerSecUSec || "",
      watcher: watcher.ActiveState || "?",
      sites: await Promise.all(sites.map(async (s) => {
        const runFile = path.join(sitesDir, s, "run");
        let running = false;
        try { const st = fs.lstatSync(runFile); running = st.isFile() && (st.mode & 0o111) !== 0; } catch {}
        const port = portList.find((p) => p[0] === name && p[1] === s)?.[2] || "";
        const u = running ? await unitProps(`site-${name}--${s}.service`, ["ActiveState", "SubState", "MainPID", "MemoryCurrent", "NRestarts"]) : {};
        return { name: s, running, port, host: `${s}.${CONFIG.BASE_DOMAIN}`, ...u };
      })),
    };
  }));
}

async function systemInfo() {
  const [free, df, caddy] = await Promise.all([
    run("free", ["-m"], { timeout: 5000 }),
    run("df", ["-h", "/", "/srv"], { timeout: 5000 }),
    unitProps("caddy.service", ["ActiveState"]),
  ]);
  return { free: free.out, df: df.out, caddy: caddy.ActiveState || "?" };
}

function tail(file, lines = 300) {
  try {
    const st = fs.statSync(file);
    const fd = fs.openSync(file, "r");
    const len = Math.min(st.size, 256 * 1024);
    const buf = Buffer.alloc(len);
    fs.readSync(fd, buf, 0, len, st.size - len);
    fs.closeSync(fd);
    return buf.toString("utf8").split("\n").slice(-lines).join("\n");
  } catch (e) { return `(no log: ${e.code || e.message})`; }
}

// ---------- pages ----------
const CSS = `
:root{color-scheme:light dark;--line:color-mix(in oklch,currentColor 18%,transparent);--muted:color-mix(in oklch,currentColor 60%,transparent)}
body{font:14px/1.45 system-ui,sans-serif;max-width:1100px;margin:0 auto;padding:16px}
h1{font-size:20px;margin:0 0 12px}h2{font-size:16px;margin:24px 0 8px}
table{border-collapse:collapse;width:100%}td,th{border-bottom:1px solid var(--line);padding:6px 8px;text-align:left;vertical-align:top}
th{font-weight:600;color:var(--muted)}.app{border:1px solid var(--line);border-radius:8px;padding:12px;margin:12px 0}
.row{display:flex;gap:8px;flex-wrap:wrap;align-items:center}.muted{color:var(--muted)}
pre{background:color-mix(in oklch,currentColor 6%,transparent);padding:10px;border-radius:6px;overflow:auto;white-space:pre-wrap;max-height:70vh}
form{display:inline}button,input{font:inherit;padding:3px 8px}input{width:8em}
.ok{color:oklch(0.6 0.15 145)}.bad{color:oklch(0.6 0.2 25)}a{color:inherit}
`;
const page = (title, body) => `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>${esc(title)}</title><style>${CSS}</style>${body}`;
const btn = (action, fields, label, confirm) =>
  `<form method=post action="/action"${confirm ? ` onsubmit="return confirm(${esc(JSON.stringify(confirm))})"` : ""}><input type=hidden name=do value="${esc(action)}">${Object.entries(fields).map(([k, v]) => `<input type=hidden name="${esc(k)}" value="${esc(v)}">`).join("")}<button>${esc(label)}</button></form>`;
const stateClass = (s) => (s === "active" ? "ok" : s ? "bad" : "muted");

async function home() {
  const [apps, sys] = await Promise.all([listApps(), systemInfo()]);
  const appsHtml = apps.map((a) => `
<div class=app>
  <div class=row><h2 style="margin:0">${esc(a.name)}</h2>
    <span class=muted>memory ${esc(a.memory || "?")} / ${esc(a.memoryMax || "?")} · cpu ${esc(a.cpu || "?")} · watcher <span class=${stateClass(a.watcher)}>${esc(a.watcher)}</span></span>
    <span style="flex:1"></span>${btn("sync", { app: a.name }, "app-sync")}
    <a href="/logs?app=${encodeURIComponent(a.name)}">sync log</a>
  </div>
  ${a.sites.length ? `<table><tr><th>site</th><th>type</th><th>state</th><th>port</th><th>memory</th><th></th></tr>
  ${a.sites.map((s) => `<tr>
    <td><a href="https://${esc(s.host)}" target=_blank rel=noopener>${esc(s.host)}</a></td>
    <td>${s.running ? "app" : "static"}</td>
    <td>${s.running ? `<span class=${stateClass(s.ActiveState)}>${esc(s.ActiveState || "?")}</span> <span class=muted>${esc(s.SubState || "")}${s.MainPID && s.MainPID !== "0" ? ` pid ${esc(s.MainPID)}` : ""}${Number(s.NRestarts) ? ` · ${esc(s.NRestarts)} restarts` : ""}</span>` : ""}</td>
    <td>${esc(s.port)}</td><td>${esc(mb(s.MemoryCurrent))}</td>
    <td class=row>${s.running ? `${btn("restart", { app: a.name, site: s.name }, "restart")}${btn("stop", { app: a.name, site: s.name }, "stop")}${btn("start", { app: a.name, site: s.name }, "start")}<a href="/logs?app=${encodeURIComponent(a.name)}&site=${encodeURIComponent(s.name)}">logs</a>` : ""}
      ${btn("rmsite", { app: a.name, site: s.name }, "remove", `Remove ${s.host}? This deletes sites/${s.name}.`)}</td>
  </tr>`).join("")}</table>` : `<p class=muted>No sites yet.</p>`}
  <details><summary class=muted>delete app</summary>
    <form method=post action="/action"><input type=hidden name=do value=delete><input type=hidden name=app value="${esc(a.name)}">
    Type <b>${esc(a.name)}</b> to delete its folder, user and everything in it: <input name=confirm autocomplete=off> <button>delete</button></form>
  </details>
</div>`).join("");

  return page("app-host", `
<div class=row><h1>app-host</h1><span class=muted>${esc(CONFIG.BASE_DOMAIN)} · caddy <span class=${stateClass(sys.caddy)}>${esc(sys.caddy)}</span></span>
<span style="flex:1"></span>${btn("syncall", {}, "app-sync --all")}${btn("caddy", {}, "reload caddy")}</div>
<h2>New app</h2>
<form method=post action="/action"><input type=hidden name=do value=create>
  <input name=app placeholder=name required pattern="[a-z][a-z0-9\\-]{1,23}" style="width:12em">
  memory <input name=memory value="${esc(CONFIG.APP_MEMORY_MAX)}"> cpu <input name=cpu value="${esc(CONFIG.APP_CPU_QUOTA)}"> tasks <input name=tasks value="${esc(CONFIG.APP_TASKS_MAX)}">
  <button>create</button></form>
<h2>Apps</h2>${appsHtml || "<p class=muted>No apps.</p>"}
<h2>System</h2><pre>${esc(sys.free)}\n${esc(sys.df)}</pre>`);
}

function resultPage(title, cmd, r, note = "") {
  return page(title, `<h1>${esc(title)}</h1><p><a href="/">← back</a></p>${note}
<p class=muted>$ ${esc(cmd)} → exit <span class=${r.code === 0 ? "ok" : "bad"}>${esc(r.code)}</span></p><pre>${esc(r.out || "(no output)")}</pre>`);
}

// ---------- actions ----------
async function action(f) {
  const app = f.get("app") || "", site = f.get("site") || "", what = f.get("do");
  if (app && !NAME_RE.test(app)) return resultPage("Bad app name", "", { code: 1, out: "invalid app name" });
  if (site && !SITE_RE.test(site)) return resultPage("Bad site name", "", { code: 1, out: "invalid site name" });
  const exists = app && fs.existsSync(path.join(APPS_DIR, app));
  const needApp = () => (!exists ? resultPage("No such app", "", { code: 1, out: app }) : null);
  const unit = `site-${app}--${site}.service`;

  switch (what) {
    case "create": {
      const mem = f.get("memory"), cpu = f.get("cpu"), tasks = f.get("tasks");
      if (!/^\d+[KMG]$/.test(mem) || !/^\d+%$/.test(cpu) || !/^\d+$/.test(tasks)) return resultPage("Bad limits", "", { code: 1, out: "memory like 256M, cpu like 50%, tasks a number" });
      const args = [app, "--memory", mem, "--cpu", cpu, "--tasks", tasks];
      return resultPage(`Created ${app}`, `app-create ${args.join(" ")}`, await run("app-create", args),
        `<p class=bad><b>This page shows the deploy private key once.</b> Copy the secrets into GitHub now; reloading won't bring them back.</p>`);
    }
    case "delete": {
      if (needApp()) return needApp();
      if (f.get("confirm") !== app) return resultPage("Not deleted", "", { code: 1, out: "confirmation didn't match the app name" });
      return resultPage(`Deleted ${app}`, `app-delete ${app}`, await run("app-delete", [app], { input: `${app}\n` }));
    }
    case "sync": if (needApp()) return needApp(); return resultPage(`Synced ${app}`, `app-sync ${app}`, await run("app-sync", [app]));
    case "syncall": return resultPage("Synced all", "app-sync --all", await run("app-sync", ["--all"]));
    case "caddy": {
      const v = await run("caddy", ["validate", "--config", "/etc/caddy/Caddyfile"]);
      if (v.code !== 0) return resultPage("Caddy config invalid, not reloaded", "caddy validate", v);
      return resultPage("Reloaded Caddy", "systemctl reload caddy", await run("systemctl", ["reload", "caddy"]));
    }
    case "restart": case "stop": case "start":
      if (needApp()) return needApp();
      return resultPage(`${what} ${site}.${CONFIG.BASE_DOMAIN}`, `systemctl ${what} ${unit}`, await run("systemctl", [what, unit]));
    case "rmsite": {
      if (needApp()) return needApp();
      const dir = path.join(APPS_DIR, app, "sites", site);
      if (!site || !fs.existsSync(dir)) return resultPage("No such site", "", { code: 1, out: dir });
      fs.rmSync(dir, { recursive: true, force: true });
      return resultPage(`Removed ${site}.${CONFIG.BASE_DOMAIN}`, `rm -rf ${dir} && app-sync ${app}`, await run("app-sync", [app]));
    }
  }
  return resultPage("Unknown action", "", { code: 1, out: String(what) });
}

async function logs(q) {
  const app = q.get("app") || "", site = q.get("site") || "";
  if (!NAME_RE.test(app) || (site && !SITE_RE.test(site))) return page("Logs", "bad name");
  const auto = `<script>scrollTo(0,document.body.scrollHeight)</script>`;
  if (site) {
    const file = path.join(APPS_DIR, app, "logs", `${site}.log`);
    const unit = await run("systemctl", ["status", "--no-pager", "-n", "0", `site-${app}--${site}.service`], { timeout: 5000 });
    return page(`${site} logs`, `<h1>${esc(site)}.${esc(CONFIG.BASE_DOMAIN)}</h1><p><a href="/">← back</a> · <a href="">refresh</a></p>
<pre>${esc(unit.out)}</pre><h2>${esc(file)}</h2><pre>${esc(tail(file))}</pre>${auto}`);
  }
  const j = await run("journalctl", ["--no-pager", "-n", "200", "-u", `app-sync@${app}.service`], { timeout: 10000 });
  return page(`${app} sync log`, `<h1>${esc(app)}: app-sync log</h1><p><a href="/">← back</a> · <a href="">refresh</a></p><pre>${esc(j.out)}</pre>${auto}`);
}

// ---------- server ----------
const expected = crypto.createHash("sha256").update(PASSWORD).digest();
let lastFail = 0;

function authed(req) {
  const m = (req.headers.authorization || "").match(/^Basic (.+)$/);
  if (!m) return false;
  const decoded = Buffer.from(m[1], "base64").toString();
  const given = crypto.createHash("sha256").update(decoded.slice(decoded.indexOf(":") + 1)).digest();
  return crypto.timingSafeEqual(given, expected);
}

http.createServer(async (req, res) => {
  const send = (code, body, headers = {}) => { res.writeHead(code, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store", "x-frame-options": "DENY", "referrer-policy": "no-referrer", ...headers }); res.end(body); };
  try {
    if (!authed(req)) {
      // slow down guessing: one failure per second, globally
      const wait = Math.max(0, lastFail + 1000 - Date.now()); lastFail = Date.now() + wait;
      await new Promise((r) => setTimeout(r, wait));
      return send(401, "login required", { "www-authenticate": 'Basic realm="app-host", charset="UTF-8"' });
    }
    const url = new URL(req.url, "http://x");
    if (req.method === "GET" && url.pathname === "/") return send(200, await home());
    if (req.method === "GET" && url.pathname === "/logs") return send(200, await logs(url.searchParams));
    if (req.method === "POST" && url.pathname === "/action") {
      // basic auth is sent on cross-site requests too, so only accept forms posted from this page
      const origin = req.headers.origin || "";
      if (!origin || new URL(origin).host !== req.headers.host) return send(403, "bad origin");
      let body = "";
      for await (const c of req) { body += c; if (body.length > 10_000) return send(413, "too big"); }
      return send(200, await action(new URLSearchParams(body)));
    }
    send(404, "not found");
  } catch (e) {
    console.error(e);
    send(500, `<pre>${esc(e.stack || e)}</pre>`);
  }
}).listen(PORT, "127.0.0.1", () => console.log(`dashboard on 127.0.0.1:${PORT}`));
