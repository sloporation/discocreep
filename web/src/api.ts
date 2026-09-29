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

/** A guild the user shares with the bot. */
export interface Guild {
  id: string;
  name: string;
  icon_url: string | null;
  /** Owner, Administrator or Manage Server: may change settings. */
  is_admin: boolean;
}

export interface GuildSettings {
  purge: PurgeSettings;
  admin_alerts: AdminAlertsSettings;
}

export interface PurgeSettings {
  on_leave: boolean;
  admin_purge: boolean;
}

export interface AdminAlertsSettings {
  channel_id: string | null;
}

export interface Channel {
  id: string;
  name: string;
}

/** An error response from the API; `code` is its "error" field. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
  ) {
    super(`API error ${status}: ${code}`);
  }
}

/** Thrown when the session is gone (logged out, expired): show the login screen. */
export class NotLoggedInError extends ApiError {}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    method,
    credentials: "include",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const code = await res
      .json()
      .then((j: { error?: string }) => j.error ?? "unknown")
      .catch(() => "unknown");
    throw res.status === 401 ? new NotLoggedInError(res.status, code) : new ApiError(res.status, code);
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

/** The logged-in user, or null if not logged in. */
export async function getMe(): Promise<User | null> {
  try {
    return await request<User>("GET", "/api/me");
  } catch (e) {
    if (e instanceof NotLoggedInError) return null;
    throw e;
  }
}

export const getGuilds = () => request<Guild[]>("GET", "/api/guilds");
export const getGuild = (id: string) => request<Guild>("GET", `/api/guilds/${id}`);
export const getSettings = (id: string) => request<GuildSettings>("GET", `/api/guilds/${id}/settings`);
export const getChannels = (id: string) => request<Channel[]>("GET", `/api/guilds/${id}/channels`);
export const savePurge = (id: string, s: PurgeSettings) =>
  request<PurgeSettings>("PUT", `/api/guilds/${id}/settings/purge`, s);
export const saveAdminAlerts = (id: string, s: AdminAlertsSettings) =>
  request<AdminAlertsSettings>("PUT", `/api/guilds/${id}/settings/admin-alerts`, s);

export const logout = () => request<void>("POST", "/auth/logout");

/** The user's linked Steam account (one per user, used in every server). */
export interface SteamAccount {
  linked: boolean;
  /** SteamID64, as a string (too big for a JavaScript number). */
  steam_id?: string;
  profile_url?: string;
  /** Only when the API has a Steam Web API key. */
  persona_name?: string;
  avatar_url?: string;
  linked_at?: string;
}

export const getSteam = () => request<SteamAccount>("GET", "/api/me/steam");
export const unlinkSteam = () => request<void>("DELETE", "/api/me/steam");

/** Where "Link Steam account" sends the browser; `next` is where to come back to. */
export const steamLinkURL = (next: string) => `${API_URL}/auth/steam/link?next=${encodeURIComponent(next)}`;
