import { createSignal } from "solid-js";

// Spotting a new dashboard: build-docs' kit update.ts. An open or installed dashboard keeps running the code it
// loaded, even after nook updates itself. Each build writes /version.json naming its entry script; this checks it on
// start, when the page comes back to the screen, and every 15 minutes.

const [ready, setReady] = createSignal(false);
export const updateReady = ready;

// this page's own build: the entry script it loaded (named by its content, so the same build gives the same name)
const mine = () => document.querySelector<HTMLScriptElement>('script[type="module"][src^="/assets/"]')?.getAttribute("src") ?? "";

let last = 0;
async function check() {
  if (ready() || !mine() || Date.now() - last < 60_000) return;
  last = Date.now();
  try {
    const res = await fetch(`/version.json?t=${Date.now()}`, { cache: "no-store" });
    if (!res.ok) return;
    const { build } = (await res.json()) as { build?: string };
    if (build && build !== mine()) setReady(true);
  } catch { /* offline: try again later */ }
}

export function watchForUpdates() {
  if (!mine()) return; // the dev server
  setTimeout(check, 10_000);
  setInterval(check, 15 * 60_000);
  document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") void check(); });
}

/** Load the new version, refreshing the service worker too. */
export async function applyUpdate() {
  try { await (await navigator.serviceWorker?.getRegistration())?.update(); } catch { /* no worker */ }
  location.reload();
}
