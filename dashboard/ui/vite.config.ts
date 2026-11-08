import { defineConfig } from "vite";
import solid from "vite-plugin-solid";

// Built files are committed to ../public so the server never has to build.
// Dev: serve the API on a unix socket (see the README) and `npm run dev`; /api is proxied to it.
const sock = process.env.NOOK_WEB_SOCK ?? "/tmp/nook-web.sock";

export default defineConfig({
  plugins: [solid()],
  build: { outDir: "../public", emptyOutDir: true, target: "es2022" },
  server: { proxy: { "/api": { target: { socketPath: sock } as any, changeOrigin: false } } },
});
