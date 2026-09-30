import { useCallback, useEffect, useState } from "react";
import { getGuilds, getMe, logout, LOGIN_URL, NotLoggedInError, type Guild, type User } from "./api";
import AccountPage from "./components/AccountPage";
import GuildList from "./components/GuildList";
import GuildPage from "./components/GuildPage";
import { useRoute } from "./router";

// Messages for the ?login_error= codes the API redirects back with.
const loginErrors: Record<string, string> = {
  access_denied: "Login was cancelled.",
  invalid_state: "That login link expired or was already used. Please try again.",
  exchange_failed: "Discord didn't accept the login. Please try again.",
  discord_error: "Couldn't reach Discord. Please try again.",
  server_error: "Something went wrong on our side. Please try again.",
};

type State =
  | { kind: "loading" }
  | { kind: "loggedOut"; message?: string }
  | { kind: "loggedIn"; user: User }
  | { kind: "error"; message: string };

export default function App() {
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    // Show (then clear from the address bar) any error from a failed login.
    const params = new URLSearchParams(window.location.search);
    const code = params.get("login_error");
    let message: string | undefined;
    if (code) {
      message = loginErrors[code] ?? `Login failed (${code}).`;
      params.delete("login_error");
      const query = params.toString();
      window.history.replaceState(null, "", window.location.pathname + (query ? `?${query}` : ""));
    }

    getMe()
      .then((user) => setState(user ? { kind: "loggedIn", user } : { kind: "loggedOut", message }))
      .catch(() => setState({ kind: "error", message: "Couldn't reach the API. Is it running?" }));
  }, []);

  // Passed to every component that calls the API: if the session is gone,
  // go back to the login screen. Returns whether it handled the error.
  const onAuthLost = useCallback((e: unknown) => {
    if (!(e instanceof NotLoggedInError)) return false;
    setState({ kind: "loggedOut", message: "Your session ended. Please log in again." });
    return true;
  }, []);

  async function handleLogout() {
    try {
      await logout();
      setState({ kind: "loggedOut" });
    } catch {
      setState({ kind: "error", message: "Couldn't log out. Please try again." });
    }
  }

  if (state.kind === "loggedIn") {
    return <Dashboard user={state.user} onLogout={handleLogout} onAuthLost={onAuthLost} />;
  }

  return (
    <main className="page">
      <section className="card">
        <h1>Discocreep</h1>
        {state.kind === "loading" && <p className="muted">Loading…</p>}
        {state.kind === "error" && <p className="error">{state.message}</p>}
        {state.kind === "loggedOut" && (
          <>
            <p className="muted">Log in with Discord to manage the bot in your servers.</p>
            {state.message && <p className="error">{state.message}</p>}
            <a className="button discord" href={LOGIN_URL}>
              Log in with Discord
            </a>
          </>
        )}
      </section>
    </main>
  );
}

function Dashboard(props: { user: User; onLogout: () => void; onAuthLost: (e: unknown) => boolean }) {
  const { user, onLogout, onAuthLost } = props;
  const [guilds, setGuilds] = useState<Guild[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [route, navigate] = useRoute();
  const selectedId = route.kind === "guild" ? route.id : null;

  useEffect(() => {
    getGuilds()
      .then(setGuilds)
      .catch((e) => {
        if (!onAuthLost(e)) setError("Couldn't load your servers.");
      });
  }, [onAuthLost]);

  // On the home page with exactly one server, there's nothing to choose: open it.
  useEffect(() => {
    if (guilds?.length === 1 && route.kind === "home") navigate({ kind: "guild", id: guilds[0].id });
  }, [guilds, route.kind, navigate]);

  return (
    <div className="dashboard">
      <header className="topbar">
        <span className="brand">Discocreep</span>
        <div className="topbar-user">
          <img className="avatar" src={user.avatar_url} alt="" width={28} height={28} />
          <span>{user.global_name ?? user.username}</span>
          <button className="button secondary small" onClick={onLogout}>
            Log out
          </button>
        </div>
      </header>

      <div className="dashboard-body">
        <aside className="sidebar">
          <h2 className="sidebar-title">Account</h2>
          <nav className="guild-list" aria-label="Account">
            <button
              className={`guild-item${route.kind === "account" ? " selected" : ""}`}
              aria-current={route.kind === "account" ? "page" : undefined}
              onClick={() => navigate({ kind: "account" })}
            >
              <span className="guild-icon placeholder nav-icon" aria-hidden>
                🔗
              </span>
              <span className="guild-name">Linked accounts</span>
            </button>
          </nav>

          <h2 className="sidebar-title">Servers</h2>
          {error && <p className="error">{error}</p>}
          {!guilds && !error && <p className="muted">Loading…</p>}
          {guilds?.length === 0 && (
            <p className="muted">The bot isn't in any server you're in. Invite it to a server first.</p>
          )}
          {guilds && guilds.length > 0 && (
            <GuildList guilds={guilds} selectedId={selectedId} onSelect={(id) => navigate({ kind: "guild", id })} />
          )}
        </aside>

        <main className="content">
          {route.kind === "account" && <AccountPage onAuthLost={onAuthLost} />}
          {route.kind === "guild" && (
            <GuildPage
              key={route.id}
              guildId={route.id}
              onAuthLost={onAuthLost}
              onGoToAccount={() => navigate({ kind: "account" })}
            />
          )}
          {route.kind === "home" && (
            <p className="muted">Choose a server on the left to see its settings, or link your accounts.</p>
          )}
        </main>
      </div>
    </div>
  );
}
