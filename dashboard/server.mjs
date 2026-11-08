// app-host dashboard: a JSON API (root-only commands) plus the static UI in ./public (built from ./ui).
// Runs as root on 127.0.0.1 behind Caddy.
// Users log in with a username + password; sessions are HMAC-signed cookies.
// New users (including the first admin) join through one-time invite links.
//   node server.mjs                        run the server (DASHBOARD_PORT, default 19999)
//   node server.mjs invite [--base URL]    print a one-time invite link (24h)
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawn } from "node:child_process";

const CONFIG_FILE = process.env.APPS_CONFIG || "/etc/apps/config";
const DASH_ENV = process.env.DASHBOARD_ENV || "/etc/apps/dashboard.env";
const GRANTS_FILE = process.env.GRANTS_FILE || "/etc/apps/grants";
const readEnvFile = (f) => {
  try {
    return Object.fromEntries(fs.readFileSync(f, "utf8").split("\n").map((l) => l.match(/^([A-Z_]+)=("?)(.*)\2$/)).filter(Boolean).map((m) => [m[1], m[3]]));
  } catch { return {}; }
};
let CONFIG = readEnvFile(CONFIG_FILE);   // re-read on every request so CLI changes show up
const PORT = Number(process.env.DASHBOARD_PORT || 19999);

const APPS_DIR = CONFIG.APPS_DIR || "/srv/apps";
const NAME_RE = /^[a-z][a-z0-9-]{1,23}$/;
const LABEL = "[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?";
const SITE_RE = new RegExp(`^(${LABEL}\\.)*${LABEL}$`);
const DOMAIN_RE = new RegExp(`^(${LABEL}\\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`);
const hostOf = (site) => (site.includes(".") ? site : `${site}.${CONFIG.BASE_DOMAIN}`);
const readGrants = () => { try { return fs.readFileSync(GRANTS_FILE, "utf8").split("\n").filter(Boolean).map((l) => l.split(" ")); } catch { return []; } };

// ---------- helpers ----------

function run(cmd, args, { input, timeout = 120_000 } = {}) {
  return new Promise((resolve) => {
    const p = spawn(cmd, args, { stdio: ["pipe", "pipe", "pipe"] });
    let out = "";
    const t = setTimeout(() => { p.kill("SIGKILL"); out += "\n[timed out]"; }, timeout);
    p.stdout.on("data", (d) => (out += d));
    p.stderr.on("data", (d) => (out += d));
    p.on("error", (e) => { clearTimeout(t); resolve({ code: -1, out: out + String(e) }); });
    p.on("close", (code) => { clearTimeout(t); resolve({ code, out }); });
    p.stdin.on("error", () => {});   // the command may exit without reading stdin
    if (input) p.stdin.end(input); else p.stdin.end();
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
    let caddy = "";
    try { caddy = fs.readFileSync(path.join(CONFIG.CADDY_APPS_DIR || "/etc/caddy/apps", `${name}.caddy`), "utf8"); } catch {}
    const live = new Set(caddy.split("\n").filter((l) => / \{$/.test(l)).map((l) => l.slice(0, -2)));
    const redirects = caddy.split("\n").filter((l) => / \{ # redirect$/.test(l)).map((l) => l.replace(/ \{ # redirect$/, ""));
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
        return { name: s, running, port, host: hostOf(s), live: live.has(hostOf(s)), ...u };
      })),
      redirects,
      grants: readGrants().filter((g) => g[0] === name),
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

// ---------- users, sessions, invites ----------
const AUTH_DIR = process.env.DASHBOARD_DIR || "/etc/apps/dashboard";
const USERS_FILE = path.join(AUTH_DIR, "users.json");
const INVITES_FILE = path.join(AUTH_DIR, "invites.json");
const SECRET_FILE = path.join(AUTH_DIR, "secret");
const SESSION_DAYS = 30, INVITE_HOURS = 24;
const USER_RE = /^[a-z0-9._-]{2,32}$/;

fs.mkdirSync(AUTH_DIR, { recursive: true, mode: 0o700 });
const readJson = (f) => { try { return JSON.parse(fs.readFileSync(f, "utf8")); } catch { return {}; } };
const writeJson = (f, v) => { fs.writeFileSync(f + ".tmp", JSON.stringify(v, null, 2), { mode: 0o600 }); fs.renameSync(f + ".tmp", f); };
if (!fs.existsSync(SECRET_FILE)) fs.writeFileSync(SECRET_FILE, crypto.randomBytes(32).toString("hex"), { mode: 0o600 });
const SECRET = fs.readFileSync(SECRET_FILE, "utf8").trim();

const sha = (s) => crypto.createHash("sha256").update(s).digest("hex");
const hmac = (s) => crypto.createHmac("sha256", SECRET).update(s).digest("base64url");
const safeEq = (a, b) => a.length === b.length && crypto.timingSafeEqual(Buffer.from(a), Buffer.from(b));

function hashPassword(pw, salt = crypto.randomBytes(16).toString("hex")) {
  return { salt, hash: crypto.scryptSync(pw, salt, 64).toString("hex") };
}
function checkPassword(user, pw) {
  const u = readJson(USERS_FILE)[user];
  // hash anyway for unknown users so timing doesn't reveal which usernames exist
  const h = hashPassword(pw, u?.salt || "0".repeat(32)).hash;
  return !!u && safeEq(h, u.hash);
}
function setPassword(user, pw, invitedBy) {
  const users = readJson(USERS_FILE);
  const prev = users[user];
  // bumping gen logs out every existing session of this user
  users[user] = { ...hashPassword(pw), gen: (prev?.gen || 0) + 1, created: prev?.created || new Date().toISOString(), invitedBy: prev?.invitedBy ?? invitedBy };
  writeJson(USERS_FILE, users);
  return users[user];
}

function makeInvite(by) {
  const token = crypto.randomBytes(24).toString("base64url");
  const invites = readJson(INVITES_FILE);
  const now = Date.now();
  for (const [k, v] of Object.entries(invites)) if (v.expires < now) delete invites[k];
  invites[sha(token)] = { by, expires: now + INVITE_HOURS * 3600_000 };
  writeJson(INVITES_FILE, invites);
  return token;
}
function getInvite(token) {
  const v = readJson(INVITES_FILE)[sha(token || "")];
  return v && v.expires > Date.now() ? v : null;
}
function useInvite(token) {
  const invites = readJson(INVITES_FILE);
  delete invites[sha(token)];
  writeJson(INVITES_FILE, invites);
}

function sessionCookie(user, gen) {
  const payload = `${user}.${gen}.${Date.now() + SESSION_DAYS * 86400_000}`;
  return `sess=${payload}.${hmac(payload)}; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=${SESSION_DAYS * 86400}`;
}
function sessionUser(req) {
  const m = (req.headers.cookie || "").match(/(?:^|;\s*)sess=([^;]+)/);
  if (!m) return null;
  const parts = m[1].split(".");
  if (parts.length !== 4) return null;
  const [user, gen, exp, sig] = parts;
  if (!safeEq(sig, hmac(`${user}.${gen}.${exp}`)) || Number(exp) < Date.now()) return null;
  const u = readJson(USERS_FILE)[user];
  return u && String(u.gen) === gen ? user : null;   // deleted users and old passwords are logged out
}
const clearCookie = "sess=; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=0";

// ---------- CLI: print an invite link (first admin, or locked out) ----------
if (process.argv[2] === "invite") {
  const i = process.argv.indexOf("--base");
  const d = readEnvFile(DASH_ENV);
  const base = i > 0 ? process.argv[i + 1] : d.DASHBOARD_HOST ? `https://${d.DASHBOARD_HOST}` : `https://${CONFIG.SSH_HOST}:${d.DASHBOARD_IP_PORT || 8443}`;
  console.log(`${base}/invite?t=${makeInvite("")}`);
  process.exit(0);
}

// ---------- API ----------
class HttpError extends Error { constructor(status, message) { super(message); this.status = status; } }
const fail = (status, message) => { throw new HttpError(status, message); };
const cmdResult = (cmd, r) => ({ ok: r.code === 0, code: r.code, cmd, out: r.out });

function appExists(app) {
  if (!NAME_RE.test(app || "")) fail(400, "invalid app name");
  if (!fs.existsSync(path.join(APPS_DIR, app))) fail(404, `no app named ${app}`);
}
function checkSite(site) { if (!SITE_RE.test(site || "")) fail(400, "invalid site name"); }

function setEnvLine(file, key, value) {
  let text = ""; try { text = fs.readFileSync(file, "utf8"); } catch {}
  const re = new RegExp(`^${key}=.*$`, "m");
  text = re.test(text) ? text.replace(re, `${key}=${value}`) : `${text.replace(/\n?$/, "\n")}${key}=${value}\n`;
  fs.writeFileSync(file, text, { mode: 0o600 });
}

function domainsInfo() {
  const dash = readEnvFile(DASH_ENV);
  const grants = readGrants().map(([app, kind, name]) => ({ app, kind, name }));
  const ip = CONFIG.SSH_HOST;
  const dns = new Map();
  for (const { kind, name } of grants) {
    if (kind !== "sub") dns.set(name, { name, note: "" });
    if (kind !== "host") dns.set(`*.${name}`, { name: `*.${name}`, note: "" });
  }
  if (dash.DASHBOARD_HOST) dns.set(dash.DASHBOARD_HOST, { name: dash.DASHBOARD_HOST, note: "dashboard" });
  let apps = [];
  try { apps = fs.readdirSync(APPS_DIR).filter((n) => NAME_RE.test(n)).sort(); } catch {}
  return {
    baseDomain: CONFIG.BASE_DOMAIN, ip, apps, grants,
    dns: [...dns.values()].map((d) => ({ type: "A", ...d, value: ip })),
    dashboardHost: dash.DASHBOARD_HOST || "",
    ipAccess: (dash.DASHBOARD_IP_ACCESS || "on") === "on",
    ipPort: dash.DASHBOARD_IP_PORT || "8443",
  };
}

function hostInUse(h) {
  const dir = CONFIG.CADDY_APPS_DIR || "/etc/caddy/apps";
  try {
    for (const fn of fs.readdirSync(dir)) {
      const lines = fs.readFileSync(path.join(dir, fn), "utf8").split("\n");
      if (lines.includes(`${h} {`) || lines.includes(`${h} { # redirect`)) return fn.replace(/\.caddy$/, "");
    }
  } catch {}
  return "";
}

// route table: [method, pattern, handler(ctx)]; patterns use :params
const routes = [];
const route = (method, pattern, handler, { open = false } = {}) => {
  const keys = [];
  const re = new RegExp("^" + pattern.replace(/:(\w+)/g, (_, k) => (keys.push(k), "([^/]+)")) + "$");
  routes.push({ method, re, keys, handler, open });
};

// --- auth (open) ---
let lastFail = 0;
const slowDown = async () => {   // one failed attempt per second, globally
  const wait = Math.max(0, lastFail + 1000 - Date.now()); lastFail = Date.now() + wait;
  await new Promise((r) => setTimeout(r, wait));
};
route("GET", "/api/session", ({ me }) => ({ user: me }), { open: true });
route("POST", "/api/login", async ({ body, setCookie }) => {
  const user = String(body.user || "");
  if (!checkPassword(user, String(body.password || ""))) { await slowDown(); fail(401, "Wrong username or password."); }
  setCookie(sessionCookie(user, readJson(USERS_FILE)[user].gen));
  return { user };
}, { open: true });
route("POST", "/api/logout", ({ setCookie }) => { setCookie(clearCookie); return {}; }, { open: true });
route("GET", "/api/invite", ({ query }) => ({ valid: !!getInvite(query.get("t")) }), { open: true });
route("POST", "/api/invite", async ({ body, setCookie }) => {
  const t = String(body.token || ""), user = String(body.user || ""), pw = String(body.password || "");
  const inv = getInvite(t);
  if (!inv) { await slowDown(); fail(410, "This invite was already used or has expired."); }
  if (!USER_RE.test(user)) fail(400, "Username: 2-32 of a-z 0-9 . _ -");
  if (readJson(USERS_FILE)[user]) fail(400, "That username is taken.");
  if (pw.length < 12) fail(400, "Password needs 12+ characters.");
  if (pw !== body.password2) fail(400, "Passwords don't match.");
  useInvite(t);
  setCookie(sessionCookie(user, setPassword(user, pw, inv.by).gen));
  return { user };
}, { open: true });

// --- apps and sites ---
route("GET", "/api/overview", async () => {
  const [apps, system] = await Promise.all([listApps(), systemInfo()]);
  return {
    baseDomain: CONFIG.BASE_DOMAIN, apps, system,
    defaults: { memory: CONFIG.APP_MEMORY_MAX, cpu: CONFIG.APP_CPU_QUOTA, tasks: CONFIG.APP_TASKS_MAX },
  };
});
route("POST", "/api/apps", async ({ body }) => {
  const { name, memory, cpu, tasks, repo } = body;
  if (!NAME_RE.test(name || "")) fail(400, "Name: a lowercase letter, then 1-23 of a-z 0-9 -");
  if (!/^\d+[KMG]$/.test(memory) || !/^\d+%$/.test(cpu) || !/^\d+$/.test(String(tasks))) fail(400, "Memory like 256M, CPU like 50%, tasks a number.");
  if (repo && !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repo)) fail(400, "GitHub repo must look like owner/name.");
  const args = [name, "--memory", memory, "--cpu", cpu, "--tasks", String(tasks), ...(repo ? ["--repo", repo] : [])];
  return cmdResult(`app create ${args.join(" ")}`, await run("app-create", args));
});
route("POST", "/api/apps/:app/delete", async ({ params, body }) => {
  appExists(params.app);
  if (body.confirm !== params.app) fail(400, "Type the app name to confirm.");
  return cmdResult(`app delete ${params.app}`, await run("app-delete", [params.app], { input: `${params.app}\n` }));
});
route("POST", "/api/apps/:app/sync", async ({ params }) => { appExists(params.app); return cmdResult(`app sync ${params.app}`, await run("app-sync", [params.app])); });
route("POST", "/api/sync-all", async () => cmdResult("app sync --all", await run("app-sync", ["--all"])));
route("POST", "/api/caddy/reload", async () => {
  const v = await run("caddy", ["validate", "--config", "/etc/caddy/Caddyfile"]);
  if (v.code !== 0) return cmdResult("caddy validate", v);
  return cmdResult("systemctl reload caddy", await run("systemctl", ["reload", "caddy"]));
});
route("POST", "/api/apps/:app/sites/:site/:op", async ({ params }) => {
  const { app, site, op } = params;
  appExists(app); checkSite(site);
  if (["restart", "stop", "start"].includes(op)) {
    const unit = `site-${app}--${site}.service`;
    return cmdResult(`systemctl ${op} ${unit}`, await run("systemctl", [op, unit]));
  }
  if (op === "remove") {
    const dir = path.join(APPS_DIR, app, "sites", site);
    if (!fs.existsSync(dir)) fail(404, "no such site");
    fs.rmSync(dir, { recursive: true, force: true });
    return cmdResult(`rm -rf ${dir} && app sync ${app}`, await run("app-sync", [app]));
  }
  fail(404, "unknown operation");
});
route("GET", "/api/apps/:app/sites/:site/logs", async ({ params }) => {
  const { app, site } = params;
  appExists(app); checkSite(site);
  const status = await run("systemctl", ["status", "--no-pager", "-n", "0", `site-${app}--${site}.service`], { timeout: 5000 });
  return { host: hostOf(site), status: status.out, log: tail(path.join(APPS_DIR, app, "logs", `${site}.log`)) };
});
route("GET", "/api/apps/:app/sync-log", async ({ params }) => {
  appExists(params.app);
  const j = await run("journalctl", ["--no-pager", "-n", "200", "-u", `app-sync@${params.app}.service`], { timeout: 10000 });
  return { log: j.out };
});

// --- runtime environment (secrets.env), via app env so the rules live in one place ---
route("GET", "/api/apps/:app/env", async ({ params }) => {
  appExists(params.app);
  const r = await run("app-env", ["list", params.app, "--values"]);
  if (r.code !== 0) fail(500, r.out);
  const vars = r.out.split("\n").filter(Boolean).map((l) => ({ key: l.slice(0, l.indexOf("=")), value: l.slice(l.indexOf("=") + 1) }));
  const running = (await listApps()).find((a) => a.name === params.app)?.sites.filter((s) => s.running).map((s) => s.host) || [];
  return { vars, running };
});
route("POST", "/api/apps/:app/env", async ({ params, body }) => {
  appExists(params.app);
  const set = body.set && typeof body.set === "object" ? body.set : {};
  const unset = Array.isArray(body.unset) ? body.unset : [];
  return cmdResult(`app env apply ${params.app}`, await run("app-env", ["apply", params.app], { input: JSON.stringify({ set, unset }) }));
});

// --- domains ---
route("GET", "/api/domains", () => domainsInfo());
route("POST", "/api/grants", async ({ body }) => {
  const op = body.op === "remove" ? "rm" : "add";
  const app = String(body.app || ""), kind = String(body.kind || ""), name = String(body.name || "").toLowerCase().trim().replace(/^\*\./, "");
  if (!NAME_RE.test(app) || !["domain", "sub", "host"].includes(kind) || !DOMAIN_RE.test(name)) fail(400, "App, kind or domain is invalid.");
  const args = [op, app, kind, name];
  return cmdResult(`app grant ${args.join(" ")}`, await run("app-grant", args));
});
route("POST", "/api/default-domain", async ({ body }) => {
  const d = String(body.domain || "").toLowerCase().trim();
  if (!DOMAIN_RE.test(d)) fail(400, "Not a valid domain.");
  const old = CONFIG.BASE_DOMAIN;
  if (d === old) return { ok: true, code: 0, cmd: "", out: "unchanged" };
  setEnvLine(CONFIG_FILE, "BASE_DOMAIN", d);
  const grants = readGrants().map((g) => (g[1] === "sub" && g[2] === old ? [g[0], "sub", d] : g));
  fs.writeFileSync(GRANTS_FILE, grants.map((g) => g.join(" ") + "\n").join(""), { mode: 0o600 });
  CONFIG = readEnvFile(CONFIG_FILE);
  const a = await run("app-admin-caddy", []), b = await run("app-sync", ["--all"]);
  return cmdResult("app-admin-caddy && app sync --all", { code: a.code || b.code, out: `${a.out}\n${b.out}` });
});
route("POST", "/api/dashboard-host", async ({ body }) => {
  const h = String(body.host || "").toLowerCase().trim();
  if (h && !DOMAIN_RE.test(h)) fail(400, "Not a valid hostname.");
  const used = h && hostInUse(h);
  if (used) fail(409, `${h} is served by app ${used}. Remove that site or pick another name.`);
  setEnvLine(DASH_ENV, "DASHBOARD_HOST", h);
  return cmdResult("app-admin-caddy", await run("app-admin-caddy", []));
});
route("POST", "/api/ip-access", async ({ body }) => {
  const on = !!body.on;
  if (!on && !readEnvFile(DASH_ENV).DASHBOARD_HOST) fail(400, "Set a dashboard address first, or you'd lock yourself out.");
  setEnvLine(DASH_ENV, "DASHBOARD_IP_ACCESS", on ? "on" : "off");
  return cmdResult("app-admin-caddy", await run("app-admin-caddy", []));
});

// --- users ---
route("GET", "/api/users", ({ me }) => ({
  users: Object.entries(readJson(USERS_FILE)).map(([name, u]) => ({ name, created: u.created || "", invitedBy: u.invitedBy || "", me: name === me })),
  inviteHours: INVITE_HOURS,
}));
route("POST", "/api/users/invite", ({ me, req }) => ({ link: `https://${req.headers.host}/invite?t=${makeInvite(me)}` }));
route("POST", "/api/users/:user/remove", ({ me, params }) => {
  const users = readJson(USERS_FILE);
  if (params.user === me) fail(400, "You can't remove yourself.");
  if (!users[params.user]) fail(404, "No such user.");
  delete users[params.user]; writeJson(USERS_FILE, users);
  return {};
});
route("POST", "/api/password", ({ me, body, setCookie }) => {
  const pw = String(body.password || "");
  if (pw.length < 12 || pw !== body.password2) fail(400, "Passwords must match and be 12+ characters.");
  setCookie(sessionCookie(me, setPassword(me, pw).gen));
  return {};
});

// ---------- static UI ----------
const UI_DIR = process.env.DASHBOARD_UI || path.join(path.dirname(new URL(import.meta.url).pathname), "public");
const TYPES = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".woff2": "font/woff2", ".json": "application/json" };
function serveStatic(url, res, headers) {
  let rel = decodeURIComponent(url.pathname);
  let file = path.join(UI_DIR, path.normalize(rel).replace(/^(\.\.[/\\])+/, ""));
  if (!file.startsWith(UI_DIR) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(UI_DIR, "index.html");   // SPA fallback
  let data;
  try { data = fs.readFileSync(file); } catch { res.writeHead(500, headers); return res.end("dashboard UI not installed"); }
  const immutable = file.includes(`${path.sep}assets${path.sep}`);
  res.writeHead(200, { ...headers, "content-type": TYPES[path.extname(file)] || "application/octet-stream", "cache-control": immutable ? "public, max-age=31536000, immutable" : "no-cache" });
  res.end(data);
}

// ---------- server ----------
process.on("uncaughtException", (e) => console.error("uncaught:", e));   // keep serving
const SECURITY = {
  "x-frame-options": "DENY", "referrer-policy": "same-origin", "x-content-type-options": "nosniff",
  "content-security-policy": "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
};

http.createServer(async (req, res) => {
  const cookies = [];
  const json = (status, value) => {
    res.writeHead(status, { ...SECURITY, "content-type": "application/json", "cache-control": "no-store", ...(cookies.length ? { "set-cookie": cookies } : {}) });
    res.end(JSON.stringify(value));
  };
  try {
    CONFIG = readEnvFile(CONFIG_FILE);
    const url = new URL(req.url, "http://x");
    if (!url.pathname.startsWith("/api/")) {
      if (req.method !== "GET" && req.method !== "HEAD") return json(405, { error: "method not allowed" });
      return serveStatic(url, res, SECURITY);
    }
    let body = {};
    if (req.method === "POST") {
      // only JSON posted from this site: a cross-site form can't send application/json without a CORS preflight
      let originHost = "";
      try { originHost = new URL(req.headers.origin || "").host; } catch {}
      if (!originHost || originHost !== req.headers.host) return json(403, { error: "bad origin" });
      if (!/^application\/json\b/.test(req.headers["content-type"] || "")) return json(415, { error: "expected application/json" });
      let raw = "";
      for await (const c of req) { raw += c; if (raw.length > 20_000) return json(413, { error: "too big" }); }
      try { body = raw ? JSON.parse(raw) : {}; } catch { return json(400, { error: "bad json" }); }
    }
    const me = sessionUser(req);
    for (const r of routes) {
      if (r.method !== req.method) continue;
      const m = url.pathname.match(r.re);
      if (!m) continue;
      if (!r.open && !me) return json(401, { error: "Logged out." });
      const params = Object.fromEntries(r.keys.map((k, i) => [k, decodeURIComponent(m[i + 1])]));
      const result = await r.handler({ req, me, params, body, query: url.searchParams, setCookie: (c) => cookies.push(c) });
      return json(200, result ?? {});
    }
    json(404, { error: "not found" });
  } catch (e) {
    if (e instanceof HttpError) return json(e.status, { error: e.message });
    console.error(e);
    json(500, { error: String(e.message || e) });
  }
}).on("clientError", (e, sock) => sock.destroy()).listen(PORT, "127.0.0.1", () => console.log(`dashboard on 127.0.0.1:${PORT}`));
