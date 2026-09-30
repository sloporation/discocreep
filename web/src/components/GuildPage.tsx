import { useEffect, useState } from "react";
import { getGuild, type Guild } from "../api";
import { GuildIcon } from "./GuildList";
import GuildSettings from "./GuildSettings";
import WowCharacterPicker from "./WowCharacterPicker";

interface Props {
  guildId: string;
  onAuthLost: (e: unknown) => boolean;
  onGoToAccount: () => void;
}

/** A selected guild: the member's WoW character, and settings (admins only). */
export default function GuildPage({ guildId, onAuthLost, onGoToAccount }: Props) {
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

      <WowCharacterPicker guildId={guild.id} onAuthLost={onAuthLost} onGoToAccount={onGoToAccount} />

      {/* Settings are for admins only; other members don't see the section at all. */}
      {guild.is_admin && <GuildSettings guildId={guild.id} onAuthLost={onAuthLost} />}
    </div>
  );
}
