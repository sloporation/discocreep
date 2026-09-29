import { useEffect, useState } from "react";
import { getSteam, steamLinkURL, unlinkSteam, type SteamAccount } from "../api";

// Messages for the ?steam_error= codes the API redirects back with.
const steamErrors: Record<string, string> = {
  cancelled: "Steam sign-in was cancelled.",
  verify_failed: "Steam couldn't confirm the sign-in. Please try again.",
  invalid_state: "That link attempt expired. Please try again.",
  not_logged_in: "Your session ended. Please log in again.",
  server_error: "Something went wrong on our side. Please try again.",
};

/** Takes (and removes from the address bar) the result of a Steam link attempt. */
function takeLinkResult(): { kind: "ok" | "error"; text: string } | null {
  const params = new URLSearchParams(window.location.search);
  const ok = params.get("steam") === "linked";
  const error = params.get("steam_error");
  if (!ok && !error) return null;
  params.delete("steam");
  params.delete("steam_error");
  const query = params.toString();
  window.history.replaceState(null, "", window.location.pathname + (query ? `?${query}` : ""));
  return ok ? { kind: "ok", text: "Steam account linked." } : { kind: "error", text: steamErrors[error!] ?? "Linking failed." };
}

/**
 * The user's Steam account: link it through Steam's own sign-in, or unlink
 * it. The link is per user, not per server (shown on the account page).
 */
export default function SteamLink({ onAuthLost }: { onAuthLost: (e: unknown) => boolean }) {
  const [account, setAccount] = useState<SteamAccount | null>(null);
  const [status, setStatus] = useState(takeLinkResult);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getSteam()
      .then(setAccount)
      .catch((e) => {
        if (!onAuthLost(e)) setStatus({ kind: "error", text: "Couldn't load your Steam account." });
      });
  }, [onAuthLost]);

  async function unlink() {
    if (!window.confirm("Unlink your Steam account? You'll stop being whitelisted until you link one again.")) return;
    setBusy(true);
    setStatus(null);
    try {
      await unlinkSteam();
      setAccount({ linked: false });
      setStatus({ kind: "ok", text: "Steam account unlinked." });
    } catch (e) {
      if (!onAuthLost(e)) setStatus({ kind: "error", text: "Couldn't unlink. Please try again." });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel">
      <h3>Steam</h3>
      <p className="muted">
        Link your Steam account so that we can whitelist you for our game servers.
      </p>

      {!account && !status && <p className="muted">Loading…</p>}

      {account?.linked && (
        <div className="steam-account">
          {account.avatar_url && <img className="avatar" src={account.avatar_url} alt="" width={40} height={40} />}
          <div className="steam-account-text">
            <a href={account.profile_url} target="_blank" rel="noreferrer">
              {account.persona_name ?? "Steam profile"}
            </a>
            <span className="muted mono">{account.steam_id}</span>
          </div>
          <button className="button secondary small" onClick={unlink} disabled={busy}>
            Unlink
          </button>
        </div>
      )}

      {account && (
        <div>
          <a className="button steam" href={steamLinkURL("/account")}>
            {account.linked ? "Change Steam account" : "Link Steam account"}
          </a>
        </div>
      )}
      {account?.linked && (
        <p className="muted">You can only link one Steam account. Changing it replaces the one above.</p>
      )}
      <p className="muted">
        We link your account to get your Steam ID, so that you can be whitelisted for pugs, to create links to CS2
        lobbies and to be whitelisted on our Ark servers.
      </p>

      {status && (
        <p className={status.kind === "ok" ? "success" : "error"} role="status">
          {status.text}
        </p>
      )}
    </section>
  );
}
