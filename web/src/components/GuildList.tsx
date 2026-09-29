import type { Guild } from "../api";

interface Props {
  guilds: Guild[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}

/** Sidebar of the guilds the user shares with the bot. */
export default function GuildList({ guilds, selectedId, onSelect }: Props) {
  return (
    <nav className="guild-list" aria-label="Servers">
      {guilds.map((g) => (
        <button
          key={g.id}
          className={`guild-item${g.id === selectedId ? " selected" : ""}`}
          aria-current={g.id === selectedId ? "page" : undefined}
          onClick={() => onSelect(g.id)}
        >
          <GuildIcon guild={g} size={32} />
          <span className="guild-name">{g.name}</span>
          {g.is_admin && <span className="badge">Admin</span>}
        </button>
      ))}
    </nav>
  );
}

export function GuildIcon({ guild, size }: { guild: Guild; size: number }) {
  if (guild.icon_url) {
    return <img className="guild-icon" src={guild.icon_url} alt="" width={size} height={size} />;
  }
  // No icon: show initials, like Discord does.
  const initials = guild.name
    .split(/\s+/)
    .map((w) => w[0])
    .join("")
    .slice(0, 3);
  return (
    <span className="guild-icon placeholder" style={{ width: size, height: size }} aria-hidden>
      {initials}
    </span>
  );
}
