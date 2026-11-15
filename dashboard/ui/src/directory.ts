import { createResource, createRoot, createSignal } from "solid-js";
import { api } from "./api";

// Every site on the server with its card (from its own web manifest), shared by the launcher and the site sheet.
export type DirCard = { host: string; url: string; app: string; listed: boolean; preview: boolean; name: string; shortName?: string; description?: string; theme?: string; background?: string; icon?: string; favicon?: string; installable: boolean };

// Fetched when the Apps page opens (not at startup, which may be the login page), and kept across pages.
const [on, setOn] = createSignal(false);
export const [directory, { refetch: refetchDirectory, mutate: setDirectory }] = createRoot(() =>
  createResource(on, () => api<{ sites: DirCard[] }>("/api/directory").then((r) => r.sites ?? []).catch(() => [] as DirCard[])),
);
export const loadDirectory = () => (on() ? refetchDirectory() : setOn(true));

export const iconURL = (c: DirCard) => (c.icon ? `/api/directory/icon/${c.icon}` : "");
export const faviconURL = (c: DirCard) => (c.favicon ? `/api/directory/icon/${c.favicon}` : "");

export async function setListed(host: string, listed: boolean) {
  setDirectory((l) => l?.map((c) => (c.host === host ? { ...c, listed } : c))); // show it at once
  try {
    await api("/api/directory/listed", { host, listed });
  } finally {
    refetchDirectory();
  }
}
