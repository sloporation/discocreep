import { useEffect, useState } from "react";
import {
  addWowGuild,
  ApiError,
  getRanks,
  getRealms,
  getRoles,
  getWowSync,
  runWowSync,
  saveWowRanks,
  removeWowGuild,
  saveWowSync,
  type DiscordRole,
  type Flavour,
  type NicknameMode,
  type RankInfo,
  type RanksResponse,
  type Realm,
  type WowGuildLink,
  type WowSyncView,
} from "../api";
import { flavourLabel } from "../wow";

const regions = [
  { id: "us", name: "Americas & Oceania (US)" },
  { id: "eu", name: "Europe (EU)" },
  { id: "kr", name: "Korea (KR)" },
  { id: "tw", name: "Taiwan (TW)" },
];

const errorMessages: Record<string, string> = {
  wow_guild_not_found: "Blizzard doesn't know that guild on that realm. Check the spelling and the realm.",
  blizzard_forbidden:
    "Blizzard refused to share this guild's roster. Blizzard's Classic Era guild API has been failing like this since late 2024.",
  role_not_assignable: "One of those roles is above the bot's role, so the bot can't give it out.",
  blizzard_error: "Couldn't reach Blizzard. Please try again in a bit.",
  battlenet_not_configured: "Battle.net isn't set up for this bot.",
  wow_guild_already_linked: "That guild is already linked.",
};

function errorMessage(e: unknown): string {
  if (e instanceof ApiError && errorMessages[e.code]) return errorMessages[e.code];
  return "Couldn't save. Please try again.";
}

type Status = { kind: "ok" | "error"; text: string } | null;
type Props = { guildId: string; onAuthLost: (e: unknown) => boolean };

/** Admin settings for keeping this server in sync with one or more WoW guilds. */
export default function WowGuildSync({ guildId, onAuthLost }: Props) {
  const [view, setView] = useState<WowSyncView | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [ranks, setRanks] = useState<RanksResponse | null>(null);
  const [roles, setRoles] = useState<DiscordRole[] | null>(null);

  useEffect(() => {
    getWowSync(guildId)
      .then(setView)
      .catch((e) => {
        if (!onAuthLost(e)) setLoadError("Couldn't load WoW guild sync settings.");
      });
  }, [guildId, onAuthLost]);

  // Ranks come from the linked guilds' rosters: reload when the list changes.
  const guildsKey = (view?.guilds ?? []).map((g) => g.id).join(",");
  useEffect(() => {
    if (!view?.battlenet_enabled || view.guilds.length === 0) return;
    setRanks(null);
    Promise.all([getRanks(guildId), roles ? Promise.resolve(roles) : getRoles(guildId)])
      .then(([rk, rl]) => {
        setRanks(rk);
        setRoles(rl);
      })
      .catch((e) => {
        if (!onAuthLost(e)) setLoadError("Couldn't load guild ranks or Discord roles.");
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [guildId, guildsKey, onAuthLost]);

  if (loadError) return <p className="error">{loadError}</p>;
  if (!view || !view.battlenet_enabled) return null;

  return (
    <section className="panel">
      <h3>WoW guild sync</h3>
      <p className="muted">
        Keep this server in step with your WoW guilds, using the mains members pick above. Only roles in the rank tables
        below are ever added or removed, members who haven't picked a main are left alone, and nothing changes for a
        guild Blizzard can't be reached for. The bot never creates or deletes roles. Ranks in different guilds can share
        a Discord role: members keep it while any of their guilds grants it.
      </p>

      <SyncSettings guildId={guildId} view={view} onSaved={setView} onAuthLost={onAuthLost} />

      <div className="subsection">
        <h4>WoW guilds</h4>
        {view.guilds.length === 0 && <p className="muted">No guilds linked yet.</p>}
        {view.guilds.map((link) => (
          <LinkedGuild
            key={link.id}
            guildId={guildId}
            link={link}
            view={view}
            ranks={ranks}
            roles={roles}
            onSaved={setView}
            onAuthLost={onAuthLost}
          />
        ))}
        <AddGuild guildId={guildId} view={view} onSaved={setView} onAuthLost={onAuthLost} />
      </div>

      {view.guilds.length > 0 && <SyncStatus guildId={guildId} view={view} onAuthLost={onAuthLost} />}
    </section>
  );
}

function SyncSettings(props: Props & { view: WowSyncView; onSaved: (v: WowSyncView) => void }) {
  const s = props.view.settings;
  const [enabled, setEnabled] = useState(s.enabled);
  const [mode, setMode] = useState<NicknameMode>(s.nickname_mode);
  const [removeRoles, setRemoveRoles] = useState(s.remove_roles);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);

  const changed = enabled !== s.enabled || mode !== s.nickname_mode || removeRoles !== s.remove_roles;

  async function save() {
    setBusy(true);
    setStatus(null);
    try {
      const v = await saveWowSync(props.guildId, { enabled, nickname_mode: mode, remove_roles: removeRoles });
      props.onSaved(v);
      setStatus({ kind: "ok", text: v.settings.enabled ? "Saved. A sync will run within a minute." : "Saved." });
    } catch (e) {
      if (!props.onAuthLost(e)) setStatus({ kind: "error", text: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  }

  const multi = props.view.flavours.length > 1;

  return (
    <div className="subsection">
      <h4>Settings</h4>
      <label className="toggle">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} disabled={busy} />
        <span>
          <strong>Sync enabled</strong>
          <span className="muted"> — update members every 15 minutes (and soon after changes)</span>
        </span>
      </label>
      <label className="toggle">
        <input type="checkbox" checked={removeRoles} onChange={(e) => setRemoveRoles(e.target.checked)} disabled={busy} />
        <span>
          <strong>Remove roles automatically</strong>
          <span className="muted">
            {" "}
            — take mapped roles away from members who no longer qualify (e.g. left the guild). Off: the sync only adds.
          </span>
        </span>
      </label>
      <label className="field">
        <span className="muted small">Nickname</span>
        <select value={mode} onChange={(e) => setMode(e.target.value as NicknameMode)} disabled={busy}>
          <option value="off">Don't change nicknames</option>
          {props.view.flavours.map((f) => (
            <option key={f} value={f}>
              {flavourLabel(f)} main's name
            </option>
          ))}
          {multi && (
            <option value="combined">
              Combined: "{props.view.flavours.map(flavourLabel).join(" / ")}" names (just one if they only have one)
            </option>
          )}
        </select>
      </label>
      <div>
        <button className="button primary" onClick={save} disabled={busy || !changed}>
          Save settings
        </button>
      </div>
      {status && <p className={status.kind === "ok" ? "success" : "error"}>{status.text}</p>}
    </div>
  );
}

const linkLabel = (l: WowGuildLink) => `${flavourLabel(l.flavour)} - ${l.realm} (${l.region.toUpperCase()})`;

/** One linked guild: its name and where it is, a Remove button, and its rank table. */
function LinkedGuild(
  props: Props & {
    link: WowGuildLink;
    view: WowSyncView;
    ranks: RanksResponse | null;
    roles: DiscordRole[] | null;
    onSaved: (v: WowSyncView) => void;
  },
) {
  const { link } = props;
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);

  async function remove() {
    if (!window.confirm(`Remove ${link.guild}? Its rank names and roles will be removed too.`)) return;
    setBusy(true);
    setStatus(null);
    try {
      props.onSaved(await removeWowGuild(props.guildId, link.id));
    } catch (e) {
      if (!props.onAuthLost(e)) setStatus({ kind: "error", text: errorMessage(e) });
      setBusy(false);
    }
  }

  return (
    <div className="guild-link">
      <div className="guild-link-header">
        <span>
          <strong>{link.guild}</strong> <span className="muted">{linkLabel(link)}</span>
        </span>
        <button className="button secondary small" onClick={remove} disabled={busy}>
          Remove
        </button>
      </div>
      {status && <p className={status.kind === "ok" ? "success" : "error"}>{status.text}</p>}
      <RankTable {...props} />
    </div>
  );
}

/** "Add guild": pick the game version, then region and realm, then type the guild name. */
function AddGuild(props: Props & { view: WowSyncView; onSaved: (v: WowSyncView) => void }) {
  const [open, setOpen] = useState(false);
  const [flavour, setFlavour] = useState<Flavour | "">("");
  const [region, setRegion] = useState("us");
  const [realm, setRealm] = useState("");
  const [guild, setGuild] = useState("");
  const [realms, setRealms] = useState<Realm[] | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open || !flavour) return;
    setRealms(null);
    getRealms(props.guildId, flavour, region)
      .then(setRealms)
      .catch((e) => {
        if (!props.onAuthLost(e)) setStatus({ kind: "error", text: "Couldn't load realms from Blizzard." });
      });
  }, [open, props.guildId, flavour, region, props.onAuthLost]);

  function close() {
    setOpen(false);
    setFlavour("");
    setRealm("");
    setGuild("");
  }

  async function save() {
    if (!flavour) return;
    setBusy(true);
    setStatus(null);
    try {
      const v = await addWowGuild(props.guildId, { flavour, region, realm_slug: realm, guild: guild.trim() });
      props.onSaved(v);
      close();
      setStatus({ kind: "ok", text: "Guild added. Name its ranks and pick roles below." });
    } catch (e) {
      if (!props.onAuthLost(e)) setStatus({ kind: "error", text: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <>
        <div>
          <button
            className="button secondary"
            onClick={() => {
              setStatus(null);
              setOpen(true);
            }}
          >
            Add guild
          </button>
        </div>
        {status && <p className={status.kind === "ok" ? "success" : "error"}>{status.text}</p>}
      </>
    );
  }

  return (
    <div className="guild-link">
      <h4>Add a guild</h4>
      <div className="form-grid">
        <label className="field">
          <span className="muted small">Game version</span>
          <select
            value={flavour}
            onChange={(e) => {
              setFlavour(e.target.value as Flavour | "");
              setRealm("");
            }}
            disabled={busy}
          >
            <option value="">Choose a version</option>
            {props.view.flavours.map((f) => (
              <option key={f} value={f}>
                {flavourLabel(f)}
              </option>
            ))}
          </select>
        </label>
        {flavour && (
          <>
            <label className="field">
              <span className="muted small">Region</span>
              <select
                value={region}
                onChange={(e) => {
                  setRegion(e.target.value);
                  setRealm("");
                }}
                disabled={busy}
              >
                {regions.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span className="muted small">Realm</span>
              <select value={realm} onChange={(e) => setRealm(e.target.value)} disabled={busy || !realms}>
                <option value="">{realms ? (realms.length ? "Choose a realm" : "No realms here") : "Loading…"}</option>
                {realms?.map((r) => (
                  <option key={r.slug} value={r.slug}>
                    {r.name}
                  </option>
                ))}
              </select>
            </label>
          </>
        )}
        {flavour && realm && (
          <label className="field">
            <span className="muted small">Guild name</span>
            <input type="text" value={guild} onChange={(e) => setGuild(e.target.value)} disabled={busy} autoFocus />
          </label>
        )}
      </div>
      <div className="row">
        <button className="button primary small" onClick={save} disabled={busy || !flavour || !realm || !guild.trim()}>
          Add
        </button>
        <button className="button secondary small" onClick={close} disabled={busy}>
          Cancel
        </button>
      </div>
      {status && <p className={status.kind === "ok" ? "success" : "error"}>{status.text}</p>}
    </div>
  );
}

/** Placeholder shown when an admin hasn't named a rank. */
const defaultRankName = (rank: number) => (rank === 0 ? "Guild Master" : `Rank ${rank}`);

function RankTable(
  props: Props & {
    link: WowGuildLink;
    view: WowSyncView;
    ranks: RanksResponse | null;
    roles: DiscordRole[] | null;
    onSaved: (v: WowSyncView) => void;
  },
) {
  const { link, view } = props;
  const rows: RankInfo[] = (props.ranks?.ranks ?? []).filter((r) => r.link_id === link.id);
  const saved = (key: string) => ({ name: view.rank_names[key] ?? "", role: view.rank_roles[key] ?? "" });
  const initial = () => Object.fromEntries(rows.map((r) => [r.key, saved(r.key)]));

  const [edits, setEdits] = useState<Record<string, { name: string; role: string }>>(initial);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);

  // Reset when the ranks load or the saved settings change.
  const savedKey = JSON.stringify([rows.map((r) => r.key), view.rank_names, view.rank_roles]);
  useEffect(() => {
    setEdits(initial());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedKey]);

  const rosterError = props.ranks?.errors?.[link.id];
  if (rosterError) {
    return <p className="error small">{errorMessages[rosterError] ?? "Couldn't read this guild's roster."}</p>;
  }
  if (!props.ranks || !props.roles) return <p className="muted small">Loading ranks…</p>;
  if (rows.length === 0) return <p className="muted small">No ranks found in this guild's roster.</p>;

  const changed = rows.some((r) => {
    const e = edits[r.key] ?? { name: "", role: "" };
    const s = saved(r.key);
    return e.name.trim() !== s.name || e.role !== s.role;
  });

  function edit(key: string, patch: Partial<{ name: string; role: string }>) {
    setEdits({ ...edits, [key]: { ...(edits[key] ?? { name: "", role: "" }), ...patch } });
  }

  async function save() {
    setBusy(true);
    setStatus(null);
    try {
      const body: Record<string, { name: string; role_id: string | null }> = {};
      for (const r of rows) {
        const e = edits[r.key] ?? { name: "", role: "" };
        body[String(r.rank)] = { name: e.name.trim(), role_id: e.role || null };
      }
      props.onSaved(await saveWowRanks(props.guildId, link.id, body));
      setStatus({ kind: "ok", text: "Saved. A sync will run within a minute." });
    } catch (e) {
      if (!props.onAuthLost(e)) setStatus({ kind: "error", text: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rank-section">
      <p className="muted small">
        Blizzard doesn't share rank names, so type them as they appear in game. The members column shows who holds each
        rank.
      </p>
      <table className="rank-table">
        <thead>
          <tr>
            <th>Rank</th>
            <th>Members</th>
            <th>Discord role</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const e = edits[r.key] ?? { name: "", role: "" };
            return (
              <tr key={r.key}>
                <td>
                  <input
                    type="text"
                    className="rank-name"
                    value={e.name}
                    placeholder={defaultRankName(r.rank)}
                    maxLength={64}
                    onChange={(ev) => edit(r.key, { name: ev.target.value })}
                    disabled={busy}
                    aria-label={`Name of rank ${r.rank}`}
                  />
                </td>
                <td>
                  {r.members}
                  <span className="muted small">
                    {" "}
                    ({r.examples.join(", ")}
                    {r.members > r.examples.length ? ", …" : ""})
                  </span>
                </td>
                <td>
                  <select
                    value={e.role}
                    onChange={(ev) => edit(r.key, { role: ev.target.value })}
                    disabled={busy}
                    aria-label={`Discord role for ${e.name.trim() || defaultRankName(r.rank)}`}
                  >
                    <option value="">No role</option>
                    {props.roles!.map((role) => (
                      <option key={role.id} value={role.id} disabled={!role.assignable}>
                        {role.name}
                        {role.assignable ? "" : " (above the bot's role)"}
                      </option>
                    ))}
                  </select>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <div>
        <button className="button primary small" onClick={save} disabled={busy || !changed}>
          Save ranks
        </button>
      </div>
      {status && <p className={status.kind === "ok" ? "success" : "error"}>{status.text}</p>}
    </div>
  );
}

function SyncStatus(props: Props & { view: WowSyncView }) {
  const st = props.view.status;
  const [message, setMessage] = useState<Status>(null);

  async function runNow() {
    try {
      await runWowSync(props.guildId);
      setMessage({ kind: "ok", text: "Sync requested; it runs within a minute. Reload to see the result." });
    } catch (e) {
      if (!props.onAuthLost(e)) setMessage({ kind: "error", text: "Couldn't request a sync." });
    }
  }

  const r = st.last_result;
  return (
    <div className="subsection">
      <h4>Last sync</h4>
      {!st.last_synced_at && <p className="muted">Hasn't run yet.</p>}
      {st.last_synced_at && (
        <p className="muted">
          {new Date(st.last_synced_at).toLocaleString()}
          {r && !st.last_error && (
            <>
              {" "}
              — {r.linked} member(s) with a main, {r.in_guild} in a linked guild, {r.updated} updated.
            </>
          )}
        </p>
      )}
      {st.last_error && <p className="error">{st.last_error}</p>}
      {!st.last_error &&
        r?.guild_errors &&
        Object.entries(r.guild_errors).map(([id, reason]) => {
          const link = props.view.guilds.find((g) => String(g.id) === id);
          return (
            <p key={id} className="error small">
              {link && <strong>{link.guild}: </strong>}
              {reason}
            </p>
          );
        })}
      {!st.last_error && (r?.errors?.length ?? 0) > 0 && (
        <details>
          <summary className="error">{r!.errors!.length} member(s) couldn't be updated</summary>
          <ul className="muted small">
            {r!.errors!.map((e, i) => (
              <li key={i}>{e}</li>
            ))}
          </ul>
        </details>
      )}
      <div>
        <button className="button secondary" onClick={runNow} disabled={!props.view.settings.enabled}>
          Sync now
        </button>
      </div>
      {message && <p className={message.kind === "ok" ? "success" : "error"}>{message.text}</p>}
    </div>
  );
}
