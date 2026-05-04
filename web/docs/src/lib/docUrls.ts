/**
 * Same rules as `web/docs/build/paths.ts` — duplicated to avoid bundling `node:fs` in the client.
 */

export function pathnameToRouteKey(pathname: string): string {
  const p = pathname.replace(/\/+$/, "") || "/";
  if (!p.startsWith("/docs")) return "";
  const rest = p.slice("/docs".length).replace(/^\/+/, "");
  return rest;
}

export function routeKeyToUrl(routeKey: string): string {
  const k = routeKey.replace(/^\/+/, "");
  return `/docs/${k}`;
}
