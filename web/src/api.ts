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
  // Some successes have no body (204, or 202 from "Sync now").
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
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
/** Ends every session of the user, on every device. */
export const logoutAll = () => request<void>("POST", "/auth/logout-all");

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

/** A WoW game version. */
export type Flavour = "retail" | "classic" | "classic_era";

/** A WoW character on the user's linked Battle.net account. */
export interface WowCharacter {
  flavour: Flavour;
  region: "us" | "eu" | "kr" | "tw";
  /** Only unique within a flavour and region. */
  character_id: number;
  name: string;
  realm: string;
  realm_slug: string;
  level: number;
  class_id: number;
  class: string;
  race: string;
  faction: "ALLIANCE" | "HORDE" | "NEUTRAL" | string;
}

export interface BattlenetAccount {
  /** False when this deployment hasn't configured Battle.net. */
  enabled: boolean;
  /** Game versions this deployment supports. */
  flavours?: Flavour[];
  linked: boolean;
  battletag?: string;
  linked_at?: string;
  synced_at?: string;
  characters?: WowCharacter[];
}

export const getBattlenet = () => request<BattlenetAccount>("GET", "/api/me/battlenet");
export const unlinkBattlenet = () => request<void>("DELETE", "/api/me/battlenet");

/** Where "Link Battle.net" / "Refresh characters" sends the browser. */
export const battlenetLinkURL = (next: string) => `${API_URL}/auth/battlenet/link?next=${encodeURIComponent(next)}`;

/** The user's main per flavour in a server (null = none chosen). */
export type Mains = Partial<Record<Flavour, WowCharacter | null>>;

export const getMains = (guildId: string) => request<{ characters: Mains }>("GET", `/api/guilds/${guildId}/wow-character`);
export const setMain = (guildId: string, c: Pick<WowCharacter, "flavour" | "region" | "character_id">) =>
  request<{ characters: Mains }>("PUT", `/api/guilds/${guildId}/wow-character`, {
    flavour: c.flavour,
    region: c.region,
    character_id: c.character_id,
  });
export const clearMain = (guildId: string, flavour: Flavour) =>
  request<void>("DELETE", `/api/guilds/${guildId}/wow-character?flavour=${flavour}`);

// --- WoW guild sync (admins) ----------------------------------------------

/** Which character name nicknames show. */
export type NicknameMode = "off" | "combined" | Flavour;

export interface WowSyncSettings {
  enabled: boolean;
  nickname_mode: NicknameMode;
  /** Remove mapped roles members no longer qualify for (off = only add). */
  remove_roles: boolean;
}

export interface WowGuildLink {
  /** ID of this link; a server can link several guilds of one flavour. */
  id: number;
  flavour: Flavour;
  region: string;
  realm_slug: string;
  realm: string;
  guild_slug: string;
  guild: string;
}

export interface WowSyncResult {
  linked: number;
  in_guild: number;
  updated: number;
  /** Link ID → why that guild's roster couldn't be read. */
  guild_errors?: Record<string, string>;
  errors?: string[];
}

export interface WowSyncStatus {
  requested_at?: string;
  last_synced_at?: string;
  last_result?: WowSyncResult;
  last_error?: string;
}

export interface WowSyncView {
  battlenet_enabled: boolean;
  flavours: Flavour[];
  settings: WowSyncSettings;
  guilds: WowGuildLink[];
  status: WowSyncStatus;
  /** "link:rank" (e.g. "12:1") → Discord role ID. */
  rank_roles: Record<string, string>;
  /** "link:rank" → admin-entered rank name (Blizzard only gives numbers). */
  rank_names: Record<string, string>;
}

export interface Realm {
  name: string;
  slug: string;
}

export interface RankInfo {
  link_id: number;
  rank: number;
  /** "link:rank", as used in rank_roles. */
  key: string;
  members: number;
  examples: string[];
}

export interface RanksResponse {
  ranks: RankInfo[];
  /** Link ID → why that guild's roster couldn't be read. */
  errors?: Record<string, string>;
}

export interface DiscordRole {
  id: string;
  name: string;
  color: number;
  position: number;
  /** The bot can give this role (below its own highest role, not managed). */
  assignable: boolean;
}

export const getWowSync = (guildId: string) => request<WowSyncView>("GET", `/api/guilds/${guildId}/wow-sync`);
export const saveWowSync = (guildId: string, s: WowSyncSettings) =>
  request<WowSyncView>("PUT", `/api/guilds/${guildId}/wow-sync`, s);
export const addWowGuild = (
  guildId: string,
  g: { flavour: Flavour; region: string; realm_slug: string; guild: string },
) => request<WowSyncView>("POST", `/api/guilds/${guildId}/wow-sync/guilds`, g);
export const removeWowGuild = (guildId: string, linkId: number) =>
  request<WowSyncView>("DELETE", `/api/guilds/${guildId}/wow-sync/guilds/${linkId}`);
export const getRealms = (guildId: string, flavour: Flavour, region: string) =>
  request<Realm[]>("GET", `/api/guilds/${guildId}/wow-sync/realms?flavour=${flavour}&region=${region}`);
export const getRanks = (guildId: string) => request<RanksResponse>("GET", `/api/guilds/${guildId}/wow-sync/ranks`);
export const getRoles = (guildId: string) => request<DiscordRole[]>("GET", `/api/guilds/${guildId}/roles`);
/** One rank's settings: its in-game name and the Discord role it gets. */
export interface RankSetting {
  name: string;
  role_id: string | null;
}

/** Saves one linked guild's rank names and roles (keyed by rank number). */
export const saveWowRanks = (guildId: string, linkId: number, ranks: Record<string, RankSetting>) =>
  request<WowSyncView>("PUT", `/api/guilds/${guildId}/wow-sync/guilds/${linkId}/ranks`, { ranks });
export const runWowSync = (guildId: string) => request<void>("POST", `/api/guilds/${guildId}/wow-sync/run`);
