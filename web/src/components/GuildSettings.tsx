import { useEffect, useState } from "react";
import {
  ApiError,
  getChannels,
  getSettings,
  saveAdminAlerts,
  savePurge,
  type Channel,
  type GuildSettings as Settings,
  type PurgeSettings,
} from "../api";
import WowGuildSync from "./WowGuildSync";

// Messages for API error codes these settings can return.
const errorMessages: Record<string, string> = {
  cannot_post_in_channel: "The bot can't post in that channel. Give it View Channel, Send Messages and Embed Links there.",
  not_a_text_channel: "That channel no longer exists or isn't a text channel.",
  not_guild_admin: "You're no longer an admin of this server.",
};

function errorMessage(e: unknown): string {
  if (e instanceof ApiError && errorMessages[e.code]) return errorMessages[e.code];
  return "Couldn't save. Please try again.";
}

/** Settings an admin can change for a guild. */
export default function GuildSettings({ guildId, onAuthLost }: { guildId: string; onAuthLost: (e: unknown) => boolean }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    setSettings(null);
    setLoadError(null);
    Promise.all([getSettings(guildId), getChannels(guildId)])
      .then(([s, c]) => {
        setSettings(s);
        setChannels(c);
      })
      .catch((e) => {
        if (!onAuthLost(e)) setLoadError("Couldn't load settings.");
      });
  }, [guildId, onAuthLost]);

  if (loadError) return <p className="error">{loadError}</p>;
  if (!settings) return <p className="muted">Loading settings…</p>;

  return (
    <>
      <AdminAlerts
        guildId={guildId}
        channels={channels}
        current={settings.admin_alerts.channel_id}
        onAuthLost={onAuthLost}
      />
      <Purge guildId={guildId} initial={settings.purge} onAuthLost={onAuthLost} />
      <WowGuildSync guildId={guildId} onAuthLost={onAuthLost} />
    </>
  );
}

function AdminAlerts(props: {
  guildId: string;
  channels: Channel[];
  current: string | null;
  onAuthLost: (e: unknown) => boolean;
}) {
  const [saved, setSaved] = useState(props.current);
  const [choice, setChoice] = useState(props.current ?? "");
  const [status, setStatus] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function save() {
    setBusy(true);
    setStatus(null);
    try {
      const res = await saveAdminAlerts(props.guildId, { channel_id: choice || null });
      setSaved(res.channel_id);
      setStatus({ kind: "ok", text: res.channel_id ? "Saved. A confirmation was posted in the channel." : "Admin alerts turned off." });
    } catch (e) {
      if (!props.onAuthLost(e)) setStatus({ kind: "error", text: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel">
      <h3>Admin alerts</h3>
      <p className="muted">Where the bot reports problems an admin needs to fix. Keep it private to admins.</p>
      <div className="row">
        <select value={choice} onChange={(e) => setChoice(e.target.value)} disabled={busy} aria-label="Admin alerts channel">
          <option value="">Off</option>
          {props.channels.map((c) => (
            <option key={c.id} value={c.id}>
              #{c.name}
            </option>
          ))}
        </select>
        <button className="button primary" onClick={save} disabled={busy || (choice || null) === saved}>
          Save
        </button>
      </div>
      {status && <p className={status.kind === "ok" ? "success" : "error"}>{status.text}</p>}
    </section>
  );
}

function Purge(props: { guildId: string; initial: PurgeSettings; onAuthLost: (e: unknown) => boolean }) {
  const [settings, setSettings] = useState(props.initial);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Each switch saves as soon as it's flipped.
  async function toggle(key: keyof PurgeSettings) {
    const next = { ...settings, [key]: !settings[key] };
    setBusy(true);
    setError(null);
    try {
      setSettings(await savePurge(props.guildId, next));
    } catch (e) {
      if (!props.onAuthLost(e)) setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel">
      <h3>Message purge</h3>
      <p className="muted">Deletes a user's messages from Discord. The audit log always keeps its copy.</p>
      <label className="toggle">
        <input type="checkbox" checked={settings.on_leave} disabled={busy} onChange={() => toggle("on_leave")} />
        <span>
          <strong>On leave</strong>
          <span className="muted"> — delete a member's messages when they leave</span>
        </span>
      </label>
      <label className="toggle">
        <input type="checkbox" checked={settings.admin_purge} disabled={busy} onChange={() => toggle("admin_purge")} />
        <span>
          <strong>Admin purge</strong>
          <span className="muted"> — allow <code>/purge user</code> in Discord</span>
        </span>
      </label>
      {error && <p className="error">{error}</p>}
    </section>
  );
}
