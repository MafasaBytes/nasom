import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev proxy: the SPA calls same-origin /api/*; Vite forwards to the Go API on :8080.
// In production the SPA is served behind a reverse proxy that maps /api to the API,
// so the frontend never hardcodes the backend origin.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
      "/healthz": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
