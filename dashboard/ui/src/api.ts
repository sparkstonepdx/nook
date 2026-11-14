import { createSignal } from "solid-js";

export type CmdResult = { ok: boolean; code: number; cmd: string; out: string };

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

// undefined = not checked yet, null = logged out
export const [user, setUser] = createSignal<string | null | undefined>(undefined);

export async function api<T = any>(path: string, body?: unknown): Promise<T> {
  const post = body !== undefined;
  const res = await fetch(path, {
    method: post ? "POST" : "GET",
    headers: post ? { "content-type": "application/json" } : {},
    body: post ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  const data = await res.json().catch(() => ({}));
  if (res.status === 401 && !path.startsWith("/api/login") && !path.startsWith("/api/invite")) setUser(null);
  if (!res.ok) throw new ApiError(res.status, data.error || res.statusText);
  return data as T;
}

// A command's output, shown in the result sheet.
export type Result = CmdResult & { title: string; warning?: string };
export const [result, setResult] = createSignal<Result | null>(null);

// Notices: one line low on the screen, above the dock. From build-docs: read time is
// about 280 ms a word plus 1.5 s, at least 3.5 s, 5 s with an action, 8 s for a problem, never over 10 s.
export type Notice = { id: number; text: string; detail?: string; kind?: "success" | "warning" | "error"; action?: { label: string; run: () => void } };
export const [notices, setNotices] = createSignal<Notice[]>([]);
let nextId = 1;

export function toastMs(text: string, opts: { bad?: boolean; actions?: boolean } = {}): number {
  const words = text.trim().split(/\s+/).filter(Boolean).length;
  const floor = opts.bad ? 8000 : opts.actions ? 5000 : 3500;
  return Math.min(10_000, Math.max(floor, 1500 + words * 280));
}

export function say(text: string, opts: Omit<Notice, "id" | "text"> = {}) {
  const n = { id: nextId++, text, ...opts };
  setNotices((l) => [...l.slice(-2), n]);
  const ms = toastMs(`${text} ${opts.detail ?? ""}`, { bad: opts.kind === "error", actions: !!opts.action });
  setTimeout(() => dismiss(n.id), ms);
}

export const dismiss = (id: number) => setNotices((l) => l.filter((n) => n.id !== id));

// runCmd runs a command on the server. A success says so in a notice, with the output a tap away; a failure,
// or anything with a warning to read, opens the output in a sheet.
export async function runCmd(title: string, path: string, body: unknown = {}, warning?: string) {
  let r: CmdResult;
  try {
    r = await api<CmdResult>(path, body);
  } catch (e) {
    r = { ok: false, code: -1, cmd: "", out: (e as Error).message };
  }
  // a failure doesn't get the success title ("Added the plugin"): the command line under it says what was tried
  const res = { ...r, title: r.ok ? title : "That didn't work", warning };
  if (!r.ok || warning) setResult(res);
  else say(title, { kind: "success", action: r.out.trim() ? { label: "Output", run: () => setResult(res) } : undefined });
  return r;
}
