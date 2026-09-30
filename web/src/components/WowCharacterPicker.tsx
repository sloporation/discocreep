import { useEffect, useState } from "react";
import {
  clearMain,
  getBattlenet,
  getMains,
  setMain,
  type BattlenetAccount,
  type Flavour,
  type Mains,
} from "../api";
import { allFlavours, characterDetail, characterKey, characterLabel, classColor, flavourLabel } from "../wow";

interface Props {
  guildId: string;
  onAuthLost: (e: unknown) => boolean;
  onGoToAccount: () => void;
}

/** The user's main for each game version in this server. */
export default function WowCharacterPicker({ guildId, onAuthLost, onGoToAccount }: Props) {
  const [account, setAccount] = useState<BattlenetAccount | null>(null);
  const [mains, setMains] = useState<Mains>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    Promise.all([getBattlenet(), getMains(guildId)])
      .then(([a, m]) => {
        setAccount(a);
        setMains(m.characters);
      })
      .catch((e) => {
        if (!onAuthLost(e)) setError("Couldn't load your WoW characters.");
      });
  }, [guildId, onAuthLost]);

  if (account && !account.enabled) return null;

  async function choose(flavour: Flavour, key: string) {
    setBusy(true);
    setError(null);
    try {
      if (key === "") {
        await clearMain(guildId, flavour);
        setMains({ ...mains, [flavour]: null });
      } else {
        const c = account?.characters?.find((ch) => characterKey(ch) === key);
        if (!c) return;
        setMains((await setMain(guildId, c)).characters);
      }
    } catch (e) {
      if (!onAuthLost(e)) setError("Couldn't save. Try refreshing your characters on Linked accounts.");
    } finally {
      setBusy(false);
    }
  }

  const flavours = account?.flavours ?? allFlavours;

  return (
    <section className="panel">
      <h3>Your WoW characters</h3>
      <p className="muted">Pick your main for each game version. They represent you in this server.</p>

      {!account && !error && <p className="muted">Loading…</p>}
      {error && <p className="error">{error}</p>}

      {account && !account.linked && (
        <>
          <p className="muted">Link your Battle.net account first.</p>
          <div>
            <button className="button battlenet" onClick={onGoToAccount}>
              Go to Linked accounts
            </button>
          </div>
        </>
      )}

      {account?.linked &&
        flavours.map((f) => {
          const chars = (account.characters ?? []).filter((c) => c.flavour === f);
          const current = mains[f] ?? null;
          return (
            <div key={f} className="main-picker">
              <label className="main-picker-label">
                <span className="muted small">{flavourLabel(f)} main</span>
                <select
                  value={current ? characterKey(current) : ""}
                  onChange={(e) => choose(f, e.target.value)}
                  disabled={busy || chars.length === 0}
                  aria-label={`Your ${flavourLabel(f)} main in this server`}
                >
                  <option value="">{chars.length === 0 ? `No ${flavourLabel(f)} characters` : "None"}</option>
                  {chars.map((c) => (
                    <option key={characterKey(c)} value={characterKey(c)}>
                      {characterLabel(c)} · {c.level} {c.class}
                    </option>
                  ))}
                </select>
              </label>
              {current && (
                <div className="character">
                  <span className="class-dot" style={{ background: classColor(current.class_id) }} aria-hidden />
                  <span className="character-text">
                    <strong>{current.name}</strong>
                    <span className="muted small">
                      {characterDetail(current)} · {current.realm} ({current.region.toUpperCase()})
                    </span>
                  </span>
                </div>
              )}
            </div>
          );
        })}
    </section>
  );
}
