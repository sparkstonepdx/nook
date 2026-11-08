// app env: runtime environment for an app's running sites, stored in /srv/apps/NAME/secrets.env.  (root)
//   app env list NAME [--values]
//   app env set NAME KEY [KEY ...]      prompts for each value (or reads lines from stdin), so values stay out of ps/history
//   app env set NAME KEY=value ...      quicker, but the value shows up in shell history
//   app env unset NAME KEY ...
//   app env apply NAME                  JSON on stdin: {"set": {"KEY": "value"}, "unset": ["KEY"]}  (used by the dashboard)
// Add --no-restart to skip restarting the app's running sites.
import fs from "node:fs";
import path from "node:path";
import readline from "node:readline";
import { execFileSync, spawnSync } from "node:child_process";

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const RESERVED = new Set(["PORT", "HOST", "SITE", "SITE_HOST", "HOME", "USER", "PATH", "SHELL", "LOGNAME"]);
const NAME_RE = /^[a-z][a-z0-9-]{1,23}$/;

const config = Object.fromEntries(fs.readFileSync("/etc/apps/config", "utf8").split("\n")
  .map((l) => l.match(/^([A-Z_]+)=("?)(.*)\2$/)).filter(Boolean).map((m) => [m[1], m[3]]));
const APPS_DIR = config.APPS_DIR || "/srv/apps";

const die = (msg) => { console.error(msg); process.exit(1); };
const usage = () => die(fs.readFileSync(new URL(import.meta.url), "utf8").split("\n").slice(0, 7).map((l) => l.replace(/^\/\/ ?/, "")).join("\n"));

// systemd EnvironmentFile: KEY="value" with \ and " escaped. No newlines.
const quote = (v) => `"${v.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
function parse(text) {
  const env = new Map();
  for (const line of text.split("\n")) {
    const m = line.match(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$/);
    if (!m) continue;
    let v = m[2].trim();
    if (/^".*"$/.test(v)) v = v.slice(1, -1).replace(/\\(["\\])/g, "$1");
    else if (/^'.*'$/.test(v)) v = v.slice(1, -1);
    env.set(m[1], v);
  }
  return env;
}

function load(app) {
  if (!NAME_RE.test(app || "")) die("usage: app env list|set|unset|apply NAME ...");
  const home = path.join(APPS_DIR, app);
  if (!fs.existsSync(home)) die(`no app named ${app}`);
  const file = path.join(home, "secrets.env");
  let text = ""; try { text = fs.readFileSync(file, "utf8"); } catch {}
  return { file, env: parse(text) };
}

function save(app, file, env) {
  const body = "# managed by app env; KEY=\"value\" lines, loaded into every running site of this app\n" +
    [...env].map(([k, v]) => `${k}=${quote(v)}\n`).join("");
  const tmp = `${file}.tmp`;
  fs.writeFileSync(tmp, body, { mode: 0o600 });
  const uid = Number(execFileSync("id", ["-u", `app-${app}`]).toString().trim());
  const gid = Number(execFileSync("id", ["-g", `app-${app}`]).toString().trim());
  fs.chownSync(tmp, uid, gid);
  fs.renameSync(tmp, file);
}

function check(key, value) {
  if (!KEY_RE.test(key)) die(`"${key}" isn't a valid name (letters, digits, _; not starting with a digit)`);
  if (RESERVED.has(key)) die(`${key} is set by app-host itself`);
  if (value !== undefined && /[\r\n\0]/.test(value)) die(`${key}: values can't contain newlines`);
}

function restart(app) {
  const sites = path.join(APPS_DIR, app, "sites");
  let restarted = [];
  for (const s of fs.existsSync(sites) ? fs.readdirSync(sites) : []) {
    if (s.startsWith(".")) continue;
    try { if (!(fs.lstatSync(path.join(sites, s, "run")).mode & 0o111)) continue; } catch { continue; }
    if (spawnSync("systemctl", ["restart", `site-${app}--${s}.service`], { stdio: "inherit" }).status === 0) restarted.push(s);
  }
  console.log(restarted.length ? `restarted: ${restarted.join(", ")}` : "no running sites to restart");
}

async function ask(keys) {
  const tty = process.stdin.isTTY;
  const rl = readline.createInterface({ input: process.stdin, output: tty ? process.stderr : undefined, terminal: tty });
  const it = rl[Symbol.asyncIterator]();
  const out = {};
  for (const k of keys) {
    if (tty) process.stderr.write(`${k}: `);
    const { value, done } = await it.next();
    if (done) die(`no value given for ${k}`);
    out[k] = value;
    if (tty) process.stderr.write("\n");
  }
  rl.close();
  return out;
}

const args = process.argv.slice(2);
const noRestart = args.includes("--no-restart");
const showValues = args.includes("--values");
const [cmd, app, ...rest] = args.filter((a) => !a.startsWith("--"));

if (cmd === "list") {
  const { env } = load(app);
  for (const [k, v] of env) console.log(showValues ? `${k}=${v}` : k);
  process.exit(0);
}

let set = {}, unset = [];
if (cmd === "set") {
  if (!rest.length) usage();
  const prompts = rest.filter((a) => !a.includes("="));
  for (const a of rest.filter((a) => a.includes("="))) set[a.slice(0, a.indexOf("="))] = a.slice(a.indexOf("=") + 1);
  prompts.forEach((k) => check(k));
  Object.assign(set, await ask(prompts));
} else if (cmd === "unset") {
  if (!rest.length) usage();
  unset = rest;
} else if (cmd === "apply") {
  let raw = ""; for await (const c of process.stdin) raw += c;
  let j; try { j = JSON.parse(raw || "{}"); } catch { die("apply: stdin must be JSON"); }
  set = j.set || {}; unset = j.unset || [];
} else usage();

const { file, env } = load(app);
for (const [k, v] of Object.entries(set)) { check(k, String(v)); env.set(k, String(v)); }
for (const k of unset) { check(k); env.delete(k); }
save(app, file, env);
const changed = [...Object.keys(set).map((k) => `set ${k}`), ...unset.map((k) => `unset ${k}`)];
console.log(changed.join(", ") || "no changes");
if (!noRestart && changed.length) restart(app);
