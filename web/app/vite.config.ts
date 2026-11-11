import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The API's origin in development. 8299, never 8099: 8099 is the container's
// published port and the node's configured target, so a local server there
// silently steals the encoder's control channel.
const apiOrigin = process.env.VITE_API_ORIGIN ?? "http://127.0.0.1:8299";

export default defineConfig({
  // The SPA owns "/": the Go binary serves index.html for every non-/api path.
  base: "/",
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    // The dev server is reached through Caddy as transcode-dev.messer.wtf /
    // transcode-dev.local, and Vite 6+ answers those with "Blocked request.
    // This host is not allowed." until they are named here — a 403 that looks
    // exactly like a broken app.
    allowedHosts: [".messer.wtf", ".local"],
    proxy: {
      "/api": apiOrigin,
    },
  },
});
