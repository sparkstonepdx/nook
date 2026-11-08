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

// the last command's output, shown in <ResultDialog>
export const [result, setResult] = createSignal<(CmdResult & { title: string; warning?: string }) | null>(null);

export async function runCmd(title: string, path: string, body: unknown = {}, warning?: string) {
  try {
    const r = await api<CmdResult>(path, body);
    setResult({ ...r, title, warning });
    return r;
  } catch (e) {
    setResult({ ok: false, code: -1, cmd: "", out: (e as Error).message, title });
  }
}
