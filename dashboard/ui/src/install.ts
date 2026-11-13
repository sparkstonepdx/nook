import { createSignal } from "solid-js";

// Installing the dashboard, and asking to: build-docs' kit install.ts (from Word Summit's), for Solid 1.
// setupInstall runs once at start.

type InstallPrompt = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: string }> };

const [installPrompt, setInstallPrompt] = createSignal<InstallPrompt | null>(null);
export { installPrompt };

const KEY = "nook";
const k = (name: string) => `${KEY}:${name}`;
const read = (name: string) => { try { return Number(localStorage.getItem(k(name)) ?? 0) || 0; } catch { return 0; } };
const write = (name: string, n: number) => { try { localStorage.setItem(k(name), String(n)); } catch { /* storage off */ } };

export function markInstalled() { write("installed", 1); }
const wasInstalled = () => read("installed") === 1;

/** Android browsers with no install prompt (Firefox) install from their menu. */
export const isAndroid = () => /android/i.test(navigator.userAgent) && !("onbeforeinstallprompt" in window);
/** iPhone and iPad add apps from Safari's Share menu. */
export const isIOS = () =>
  /iphone|ipad|ipod/i.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
export const isStandalone = () =>
  matchMedia("(display-mode: standalone), (display-mode: fullscreen), (display-mode: minimal-ui), (display-mode: window-controls-overlay)").matches ||
  (navigator as Navigator & { standalone?: boolean }).standalone === true;

const [visits, setVisits] = createSignal(0);
const [askHidden, setAskHidden] = createSignal(false);
const AWAY_MS = 30 * 60_000;

export function setupInstall() {
  setVisits(read("visits"));
  window.addEventListener("beforeinstallprompt", (e) => { e.preventDefault(); setInstallPrompt(e as InstallPrompt); });
  window.addEventListener("appinstalled", () => { setInstallPrompt(null); markInstalled(); });
  // the manifest opens the installed app at /?app: the one sure sign in every browser
  if (new URLSearchParams(location.search).has("app")) {
    markInstalled();
    const u = new URL(location.href);
    u.searchParams.delete("app");
    history.replaceState(history.state, "", u.pathname + u.search + u.hash);
  }
  if ("serviceWorker" in navigator) {
    const go = () => navigator.serviceWorker.register("/sw.js").catch(() => { /* a self-signed address can't have one; the page still works */ });
    if (document.readyState === "complete") go();
    else window.addEventListener("load", go, { once: true });
  }
  if (isStandalone()) markInstalled();
  const seen = () => {
    const now = Date.now(), lastSeen = read("last-seen");
    if (!lastSeen || now - lastSeen > AWAY_MS) { write("visits", read("visits") + 1); setVisits(read("visits")); }
    write("last-seen", now);
  };
  seen();
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") seen();
    else write("last-seen", Date.now());
  });
}

export async function install() {
  const p = installPrompt();
  if (!p) return;
  await p.prompt();
  await p.userChoice;
  setInstallPrompt(null);
}

/** From the second visit, where the browser can install, unless put off or already installed. */
export const shouldAskInstall = () =>
  !askHidden() && !isStandalone() && !wasInstalled() && visits() >= 2 && visits() >= read("install-later") &&
  (!!installPrompt() || isIOS() || isAndroid());

/** Not now: ask again five visits from now. */
export function askLater() {
  write("install-later", read("visits") + 5);
  setAskHidden(true);
}
