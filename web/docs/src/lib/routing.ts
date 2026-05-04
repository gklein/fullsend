import { pathnameToRouteKey, routeKeyToUrl } from "./docUrls";

export function getRouteKeyFromLocation(): string {
  return pathnameToRouteKey(window.location.pathname);
}

export function navigateToRouteKey(key: string): void {
  const url = key === "" ? "/docs/" : routeKeyToUrl(key);
  if (url !== window.location.pathname) {
    history.pushState(null, "", url);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }
}

export function defaultRouteKey(pages: Record<string, unknown>): string | null {
  const keys = Object.keys(pages).sort((a, b) => a.localeCompare(b));
  const vision = keys.find((k) => k === "vision");
  if (vision) return vision;
  return keys[0] ?? null;
}
