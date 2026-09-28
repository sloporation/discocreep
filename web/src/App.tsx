import { useEffect, useState } from "react";
import { getMe, logout, LOGIN_URL, type User } from "./api";

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
  | { kind: "loggedOut" }
  | { kind: "loggedIn"; user: User }
  | { kind: "error"; message: string };

export default function App() {
  const [state, setState] = useState<State>({ kind: "loading" });
  const [loginError, setLoginError] = useState<string | null>(null);

  useEffect(() => {
    // Show (then clear from the address bar) any error from a failed login.
    const params = new URLSearchParams(window.location.search);
    const code = params.get("login_error");
    if (code) {
      setLoginError(loginErrors[code] ?? `Login failed (${code}).`);
      params.delete("login_error");
      const query = params.toString();
      window.history.replaceState(null, "", window.location.pathname + (query ? `?${query}` : ""));
    }

    getMe()
      .then((user) => setState(user ? { kind: "loggedIn", user } : { kind: "loggedOut" }))
      .catch(() => setState({ kind: "error", message: "Couldn't reach the API. Is it running?" }));
  }, []);

  async function handleLogout() {
    try {
      await logout();
      setState({ kind: "loggedOut" });
    } catch {
      setState({ kind: "error", message: "Couldn't log out. Please try again." });
    }
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
            {loginError && <p className="error">{loginError}</p>}
            <a className="button discord" href={LOGIN_URL}>
              Log in with Discord
            </a>
          </>
        )}

        {state.kind === "loggedIn" && (
          <>
            <div className="user">
              <img className="avatar" src={state.user.avatar_url} alt="" width={64} height={64} />
              <div>
                <div className="name">{state.user.global_name ?? state.user.username}</div>
                <div className="muted">@{state.user.username}</div>
              </div>
            </div>
            <p className="muted">You're logged in. Server settings are coming soon.</p>
            <button className="button secondary" onClick={handleLogout}>
              Log out
            </button>
          </>
        )}
      </section>
    </main>
  );
}
