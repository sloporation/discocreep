import { useState } from "react";
import BattlenetLink from "./BattlenetLink";
import SteamLink from "./SteamLink";

interface Props {
  onAuthLost: (e: unknown) => boolean;
  onLogoutAll: () => Promise<boolean>;
}

/** The user's linked accounts and sessions. These belong to the user, not to a server. */
export default function AccountPage({ onAuthLost, onLogoutAll }: Props) {
  return (
    <div className="guild-page">
      <header className="guild-header">
        <h2>Linked accounts</h2>
      </header>
      <p className="muted">Accounts linked here apply in every server this bot is in.</p>
      <SteamLink onAuthLost={onAuthLost} />
      <BattlenetLink onAuthLost={onAuthLost} />
      <Sessions onLogoutAll={onLogoutAll} />
    </div>
  );
}

function Sessions({ onLogoutAll }: { onLogoutAll: () => Promise<boolean> }) {
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  async function logoutEverywhere() {
    if (!window.confirm("Log out on every device, including this one?")) return;
    setBusy(true);
    setFailed(false);
    if (!(await onLogoutAll())) {
      setFailed(true);
      setBusy(false);
    }
  }

  return (
    <section className="panel">
      <h3>Sessions</h3>
      <p className="muted">
        Logged in somewhere you shouldn't be, like a shared computer? This ends every session, on every device.
      </p>
      <div>
        <button className="button secondary" onClick={logoutEverywhere} disabled={busy}>
          Log out of all devices
        </button>
      </div>
      {failed && <p className="error">Couldn't log out everywhere. Please try again.</p>}
    </section>
  );
}
