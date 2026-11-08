// app-host deploy action. No dependencies: Node built-ins plus ssh and rsync (both on GitHub's runners).
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

const input = (name) => (process.env[`INPUT_${name.replace(/ /g, "_").toUpperCase()}`] || "").trim();
const fail = (msg) => { console.log(`::error::${msg}`); process.exit(1); };
const setOutput = (k, v) => process.env.GITHUB_OUTPUT && fs.appendFileSync(process.env.GITHUB_OUTPUT, `${k}=${v}\n`);
const summary = (md) => process.env.GITHUB_STEP_SUMMARY && fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, md + "\n");

// --- the APP_HOST secret ---
const raw = input("app-host");
if (!raw) fail("app-host is empty: add the APP_HOST secret to this repo (it's printed by `app create`).");
if (!raw.startsWith("apphost1:")) fail("app-host doesn't look like an APP_HOST secret (expected it to start with apphost1:).");
let cfg;
try { cfg = JSON.parse(Buffer.from(raw.slice(9), "base64url").toString("utf8")); } catch { fail("APP_HOST secret is damaged; copy it again from `app create`."); }
for (const k of ["host", "user", "domain", "knownHosts", "key"]) if (!cfg[k]) fail(`APP_HOST secret is missing ${k}.`);

// --- which site ---
const LABEL = "[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?";
const SITE_RE = new RegExp(`^(${LABEL}\\.)*${LABEL}$`);
const site = input("site").toLowerCase();
if (!SITE_RE.test(site)) fail(`site "${site}" must be a subdomain like "blog" or a hostname like "client.com".`);

const event = process.env.GITHUB_EVENT_NAME || "";
const payload = process.env.GITHUB_EVENT_PATH ? JSON.parse(fs.readFileSync(process.env.GITHUB_EVENT_PATH, "utf8")) : {};
const pr = event.startsWith("pull_request") && input("previews") !== "false" ? payload.pull_request?.number ?? payload.number : null;
const closing = pr != null && payload.action === "closed";
// previews: "myapp" -> "myapp-pr-12" (default domain); "client.com" -> "pr-12.client.com"
const name = pr == null ? site : site.includes(".") ? `pr-${pr}.${site.replace(/^www\./, "")}` : `${site}-pr-${pr}`;
const hostname = name.includes(".") ? name : `${name}.${cfg.domain}`;
const url = `https://${hostname}`;

// --- ssh setup (key never printed) ---
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "app-host-"));
const keyFile = path.join(tmp, "key"), khFile = path.join(tmp, "known_hosts");
fs.writeFileSync(keyFile, cfg.key.endsWith("\n") ? cfg.key : cfg.key + "\n", { mode: 0o600 });
fs.writeFileSync(khFile, cfg.knownHosts.split("\n").map((l) => (cfg.port && cfg.port !== 22 && !l.startsWith("[") ? l.replace(/^(\S+)/, `[$1]:${cfg.port}`) : l)).join("\n") + "\n");
const sshOpts = ["-i", keyFile, "-o", `UserKnownHostsFile=${khFile}`, "-o", "StrictHostKeyChecking=yes", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=20", "-p", String(cfg.port || 22)];
const target = `${cfg.user}@${cfg.host}`;
const sh = (label, cmd, args) => { console.log(`$ ${label}`); return spawnSync(cmd, args, { stdio: "inherit" }).status; };
const ssh = (remote) => sh(`ssh ${target} ${remote}`, "ssh", [...sshOpts, target, remote]);
const cleanup = () => fs.rmSync(tmp, { recursive: true, force: true });

async function comment(body) {
  const token = input("github-token");
  const repo = process.env.GITHUB_REPOSITORY;
  if (!token || !repo || pr == null) return;
  const api = (p, init = {}) => fetch(`${process.env.GITHUB_API_URL || "https://api.github.com"}/repos/${repo}${p}`, {
    ...init, headers: { authorization: `Bearer ${token}`, accept: "application/vnd.github+json", "content-type": "application/json", "user-agent": "app-host-deploy" },
  });
  const marker = `<!-- app-host:${site} -->`;
  try {
    const list = await (await api(`/issues/${pr}/comments?per_page=100`)).json();
    const mine = Array.isArray(list) ? list.find((c) => c.body?.includes(marker)) : null;
    const res = mine
      ? await api(`/issues/comments/${mine.id}`, { method: "PATCH", body: JSON.stringify({ body: `${marker}\n${body}` }) })
      : await api(`/issues/${pr}/comments`, { method: "POST", body: JSON.stringify({ body: `${marker}\n${body}` }) });
    if (!res.ok) console.log(`::warning::Couldn't comment on the PR (${res.status}). Give the workflow "pull-requests: write" permission.`);
  } catch (e) { console.log(`::warning::Couldn't comment on the PR: ${e.message}`); }
}

try {
  if (closing) {
    if (ssh(`app site rm ${name}`) !== 0) fail(`Couldn't remove ${hostname}.`);
    await comment(`Preview removed: ~~${url}~~`);
    summary(`Removed preview **${hostname}**`);
  } else {
    const dir = input("dir") || "dist";
    if (!fs.existsSync(dir) || !fs.statSync(dir).isDirectory()) fail(`dir "${dir}" doesn't exist. Did the build step run?`);
    const upload = `builds/${name}/`;
    const rsyncSsh = ["ssh", ...sshOpts].map((a) => (/\s/.test(a) ? `'${a}'` : a)).join(" ");
    const src = dir.replace(/\/?$/, "/");
    if (sh(`rsync ${src} ${target}:${upload}`, "rsync", ["-a", "--delete", "-e", rsyncSsh, src, `${target}:${upload}`]) !== 0) fail("Upload failed.");
    const code = ssh(`app site deploy ${upload} ${name}`);
    if (code === 2) fail(`${hostname} didn't go live: this app isn't allowed that hostname, it's reserved, or another app has it. Grant it on the dashboard's Domains page.`);
    if (code !== 0) fail(`Deploy failed (exit ${code}).`);
    setOutput("url", url); setOutput("site", name);
    summary(`Deployed **[${hostname}](${url})**${pr != null ? ` (preview for #${pr})` : ""}`);
    if (pr != null) await comment(`Preview: ${url}\n\n<sub>Updated for ${(process.env.GITHUB_SHA || "").slice(0, 7)}</sub>`);
    console.log(`Live at ${url}`);
  }
} finally { cleanup(); }
