import { useEffect, useState } from "react";
import { getGuild, type Guild } from "../api";
import { GuildIcon } from "./GuildList";
import GuildSettings from "./GuildSettings";

/** A selected guild: its settings (editable by admins only). */
export default function GuildPage({ guildId, onAuthLost }: { guildId: string; onAuthLost: (e: unknown) => boolean }) {
  const [guild, setGuild] = useState<Guild | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setGuild(null);
    setError(null);
    getGuild(guildId)
      .then(setGuild)
      .catch((e) => {
        if (onAuthLost(e)) return;
        setError("This server isn't available. The bot may have left it, or you're no longer a member.");
      });
  }, [guildId, onAuthLost]);

  if (error) return <p className="error">{error}</p>;
  if (!guild) return <p className="muted">Loading…</p>;

  return (
    <div className="guild-page">
      <header className="guild-header">
        <GuildIcon guild={guild} size={48} />
        <h2>{guild.name}</h2>
      </header>

      {guild.is_admin ? (
        <GuildSettings guildId={guild.id} onAuthLost={onAuthLost} />
      ) : (
        <section className="panel">
          <h3>Settings</h3>
          <p className="muted">Only server admins (Manage Server or Administrator) can change the bot's settings here.</p>
        </section>
      )}
    </div>
  );
}
