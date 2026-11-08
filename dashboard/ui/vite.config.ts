import { defineConfig } from "vite";
import solid from "vite-plugin-solid";

// Built files are committed to ../public so the server never has to build.
// Dev: run the API locally on :19999 and `npm run dev`; /api is proxied.
export default defineConfig({
  plugins: [solid()],
  build: { outDir: "../public", emptyOutDir: true, target: "es2022" },
  server: { proxy: { "/api": "http://127.0.0.1:19999" } },
});
