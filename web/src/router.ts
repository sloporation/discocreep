import { useCallback, useEffect, useState } from "react";

// A tiny router (no library). Routes:
//   /              home
//   /account       the user's linked accounts
//   /guilds/<id>   a server
// The web server sends every path to index.html, so these URLs can be
// bookmarked and reloaded.

export type Route = { kind: "home" } | { kind: "account" } | { kind: "guild"; id: string };

export function routePath(route: Route): string {
  switch (route.kind) {
    case "home":
      return "/";
    case "account":
      return "/account";
    case "guild":
      return `/guilds/${route.id}`;
  }
}

function routeFromPath(path: string): Route {
  if (/^\/account\/?$/.test(path)) return { kind: "account" };
  const m = path.match(/^\/guilds\/(\d+)\/?$/);
  if (m) return { kind: "guild", id: m[1] };
  return { kind: "home" };
}

/** The current route (from the URL) and a function to navigate. */
export function useRoute(): [Route, (route: Route) => void] {
  const [route, setRoute] = useState(() => routeFromPath(window.location.pathname));

  useEffect(() => {
    const onPop = () => setRoute(routeFromPath(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const navigate = useCallback((next: Route) => {
    const path = routePath(next);
    if (window.location.pathname !== path) window.history.pushState(null, "", path);
    setRoute(next);
  }, []);

  return [route, navigate];
}
