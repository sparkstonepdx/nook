import { createSignal } from "solid-js";

// Per-browser conveniences (open rows, a toggle): a failed read is the fallback, a failed write is ignored.
export function readStored<T>(key: string, fallback: T): T {
  try { const v = localStorage.getItem(key); return v == null ? fallback : (JSON.parse(v) as T); } catch { return fallback; }
}
export function writeStored(key: string, value: unknown) {
  try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* storage off */ }
}

// A signal kept in localStorage.
export function storedSignal<T>(key: string, fallback: T) {
  const [get, set] = createSignal<T>(readStored(key, fallback));
  return [get, (v: T) => { set(() => v); writeStored(key, v); }] as const;
}
