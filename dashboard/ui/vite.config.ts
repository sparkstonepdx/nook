import { defineConfig, type Plugin } from "vite";
import solid from "vite-plugin-solid";

// Built files are committed to ../public so the server never has to build.
// Dev: serve the API on a unix socket (see the README) and `npm run dev`; /api is proxied to it.
const sock = process.env.NOOK_WEB_SOCK ?? "/tmp/nook-web.sock";

// version.json names the entry script, so an open dashboard can tell it's out of date (src/update.ts). Content-named,
// so the same source builds the same file and CI's "dashboard/public is current" check still holds.
function versionFile(): Plugin {
  return {
    name: "version-file",
    generateBundle(_, bundle) {
      const entry = Object.values(bundle).find((c) => c.type === "chunk" && c.isEntry);
      if (entry) this.emitFile({ type: "asset", fileName: "version.json", source: JSON.stringify({ build: `/${entry.fileName}` }) + "\n" });
    },
  };
}

export default defineConfig({
  plugins: [solid(), versionFile()],
  build: { outDir: "../public", emptyOutDir: true, target: "es2022" },
  server: { proxy: { "/api": { target: { socketPath: sock } as any, changeOrigin: false } } },
});
