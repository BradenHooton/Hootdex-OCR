import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the Go API runs on :8080; Vite proxies /api (including the SSE stream).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: false },
      "/healthz": "http://localhost:8080",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    chunkSizeWarningLimit: 600,
  },
});
