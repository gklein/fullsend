/** Parsed app fragment: `#/<routeKey>` or `#/<routeKey>::<slug>` (leading `#` optional when parsing). */
export type DocHashRoute = {
  routeKey: string;
  slug?: string;
};

/**
 * Empty hash or `#/` means “use default document” (caller resolves).
 */
export function parseDocHash(hash: string): DocHashRoute | null {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash;
  if (raw === "" || raw === "/") return null;

  const withoutLead = raw.startsWith("/") ? raw.slice(1) : raw;
  const sep = withoutLead.indexOf("::");
  if (sep === -1) {
    return { routeKey: withoutLead };
  }
  return {
    routeKey: withoutLead.slice(0, sep),
    slug: withoutLead.slice(sep + 2),
  };
}

export function formatDocHash(routeKey: string, slug?: string): string {
  const k = routeKey.replace(/^\/+/, "");
  if (!k) return "#/";
  return slug !== undefined && slug !== ""
    ? `#/${k}::${slug}`
    : `#/${k}`;
}
