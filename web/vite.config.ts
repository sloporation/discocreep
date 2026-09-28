import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The app is a static site: `npm run build` writes plain files to dist/ that
// any static host can serve. It talks to the Go API (VITE_API_URL) from the
// browser.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // The API's api.web_url must match this origin exactly (CORS), so don't
    // silently move to another port.
    strictPort: true,
  },
});
