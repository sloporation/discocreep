// Runtime config, loaded before the app. The web container overwrites this
// file at startup from BXT_API_URL, so one image works for any API URL.
// Left empty here so `npm run dev` falls back to VITE_API_URL.
window.__BXT_CONFIG__ = { apiUrl: "" };
