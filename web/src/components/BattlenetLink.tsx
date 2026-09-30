import { useEffect, useState } from "react";
import { battlenetLinkURL, getBattlenet, unlinkBattlenet, type BattlenetAccount } from "../api";
import { allFlavours, characterDetail, characterKey, classColor, flavourLabel } from "../wow";
import type { Flavour } from "../api";

// Messages for the ?battlenet_error= codes the API redirects back with.
const errors: Record<string, string> = {
  cancelled: "Battle.net sign-in was cancelled.",
  exchange_failed: "Battle.net didn't accept the sign-in. Please try again.",
  blizzard_error: "Couldn't read your characters from Blizzard. Please try again in a bit.",
  invalid_state: "That link attempt expired. Please try again.",
  not_logged_in: "Your session ended. Please log in again.",
  server_error: "Something went wrong on our side. Please try again.",
};

function takeLinkResult(): { kind: "ok" | "error"; text: string } | null {
  const params = new URLSearchParams(window.location.search);
  const ok = params.get("battlenet") === "linked";
  const error = params.get("battlenet_error");
  const skipped = (params.get("battlenet_skipped") ?? "").split(",").filter(Boolean) as Flavour[];
  if (!ok && !error) return null;
  params.delete("battlenet");
  params.delete("battlenet_error");
  params.delete("battlenet_skipped");
  const query = params.toString();
  window.history.replaceState(null, "", window.location.pathname + (query ? `?${query}` : ""));
  if (ok && skipped.length > 0) {
    return {
      kind: "error",
      text: `Battle.net linked, but Blizzard couldn't give us your ${skipped.map(flavourLabel).join(" and ")} characters right now; we kept the ones we had. Try refreshing later.`,
    };
  }
  return ok
    ? { kind: "ok", text: "Battle.net linked. Your characters are up to date." }
    : { kind: "error", text: errors[error!] ?? "Linking failed." };
}

/** The user's Battle.net account and its retail WoW characters. */
export default function BattlenetLink({ onAuthLost }: { onAuthLost: (e: unknown) => boolean }) {
  const [account, setAccount] = useState<BattlenetAccount | null>(null);
  const [status, setStatus] = useState(takeLinkResult);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getBattlenet()
      .then(setAccount)
      .catch((e) => {
        if (!onAuthLost(e)) setStatus({ kind: "error", text: "Couldn't load your Battle.net account." });
      });
  }, [onAuthLost]);

  // This deployment doesn't have Battle.net set up.
  if (account && !account.enabled) return null;

  async function unlink() {
    if (!window.confirm("Unlink Battle.net? Your characters and your character choices in every server will be removed.")) {
      return;
    }
    setBusy(true);
    setStatus(null);
    try {
      await unlinkBattlenet();
      setAccount({ enabled: true, linked: false });
      setStatus({ kind: "ok", text: "Battle.net unlinked." });
    } catch (e) {
      if (!onAuthLost(e)) setStatus({ kind: "error", text: "Couldn't unlink. Please try again." });
    } finally {
      setBusy(false);
    }
  }

  const chars = account?.characters ?? [];

  return (
    <section className="panel">
      <h3>World of Warcraft</h3>
      <p className="muted">
        Link your Battle.net account to choose which characters represent you in each server (a main for each game
        version).
      </p>

      {!account && !status && <p className="muted">Loading…</p>}

      {account?.linked && (
        <>
          <div className="steam-account">
            <div className="steam-account-text">
              <strong>{account.battletag}</strong>
              {account.synced_at && (
                <span className="muted">Characters updated {new Date(account.synced_at).toLocaleString()}</span>
              )}
            </div>
            <button className="button secondary small" onClick={unlink} disabled={busy}>
              Unlink
            </button>
          </div>

          {chars.length === 0 ? (
            <p className="muted">No characters found on this account.</p>
          ) : (
            (account.flavours ?? allFlavours).map((f) => {
              const fc = chars.filter((c) => c.flavour === f);
              if (fc.length === 0) return null;
              return (
                <div key={f} className="flavour-group">
                  <h4>{flavourLabel(f)}</h4>
                  <ul className="character-list">
                    {fc.map((c) => (
                      <li key={characterKey(c)} className="character">
                        <span className="class-dot" style={{ background: classColor(c.class_id) }} aria-hidden />
                        <span className="character-text">
                          <span>
                            <strong>{c.name}</strong> <span className="muted">- {c.realm}</span>
                          </span>
                          <span className="muted small">
                            {characterDetail(c)} · {c.region.toUpperCase()}
                          </span>
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              );
            })
          )}
        </>
      )}

      {account && (
        <div>
          <a className="button battlenet" href={battlenetLinkURL("/account")}>
            {account.linked ? "Refresh characters" : "Link Battle.net"}
          </a>
        </div>
      )}
      {account?.linked && (
        <p className="muted">
          Made a new character or transferred one? Refresh to fetch the latest list from Blizzard.
        </p>
      )}

      {status && (
        <p className={status.kind === "ok" ? "success" : "error"} role="status">
          {status.text}
        </p>
      )}
    </section>
  );
}
