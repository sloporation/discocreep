// Client for the Go API. Every call sends the session cookie
// (credentials: "include"); the API only accepts requests from this app's
// origin.

declare global {
  interface Window {
    // Set by /config.js; the web container writes it from BXT_API_URL.
    __BXT_CONFIG__?: { apiUrl?: string };
  }
}

// The API's URL: runtime config (container) first, then the build-time
// VITE_API_URL (npm run dev), then the local default.
export const API_URL: string = (
  window.__BXT_CONFIG__?.apiUrl || import.meta.env.VITE_API_URL || "http://localhost:8080"
).replace(/\/$/, "");

/** Where the "Log in with Discord" button sends the browser. */
export const LOGIN_URL = `${API_URL}/auth/login`;

export interface User {
  id: string;
  username: string;
  global_name: string | null;
  avatar_url: string;
}

/** The logged-in user, or null if not logged in. */
export async function getMe(): Promise<User | null> {
  const res = await fetch(`${API_URL}/api/me`, { credentials: "include" });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(`GET /api/me failed: ${res.status}`);
  return res.json();
}

export async function logout(): Promise<void> {
  const res = await fetch(`${API_URL}/auth/logout`, { method: "POST", credentials: "include" });
  if (!res.ok) throw new Error(`POST /auth/logout failed: ${res.status}`);
}
