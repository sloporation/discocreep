import BattlenetLink from "./BattlenetLink";
import SteamLink from "./SteamLink";

/** The user's linked accounts. These belong to the user, not to a server. */
export default function AccountPage({ onAuthLost }: { onAuthLost: (e: unknown) => boolean }) {
  return (
    <div className="guild-page">
      <header className="guild-header">
        <h2>Linked accounts</h2>
      </header>
      <p className="muted">Accounts linked here apply in every server this bot is in.</p>
      <SteamLink onAuthLost={onAuthLost} />
      <BattlenetLink onAuthLost={onAuthLost} />
    </div>
  );
}
