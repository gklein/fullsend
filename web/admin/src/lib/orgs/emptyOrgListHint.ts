/**
 * Heuristics for `GET /user/orgs` when GitHub returns **no rows**.
 *
 * GitHub’s OpenAPI notes fine-grained tokens respond with **200 + empty list** when org data
 * is not accessible (instead of 403). Classic OAuth missing scope tends to yield **403**.
 */

export function headersToRecord(headers: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (!headers) return out;
  if (typeof Headers !== "undefined" && headers instanceof Headers) {
    headers.forEach((value, key) => {
      out[key.toLowerCase()] = value;
    });
    return out;
  }
  if (typeof headers === "object") {
    for (const [k, v] of Object.entries(headers as Record<string, unknown>)) {
      if (typeof v === "string") out[k.toLowerCase()] = v;
    }
  }
  return out;
}

function parseOAuthScopes(raw: string | undefined): string[] {
  if (!raw?.trim()) return [];
  return raw
    .split(/[, ]+/)
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
}

function canListOrgsWithClassicScopes(scopes: string[]): boolean {
  return scopes.some(
    (s) =>
      s === "read:org" ||
      s === "user" ||
      s === "read:user" ||
      s === "write:org" ||
      s === "admin:org",
  );
}

/**
 * When the org list is empty, returns user-facing copy explaining likely causes from HTTP status
 * and GitHub response headers. Returns `null` when there is nothing specific to add.
 */
export function buildEmptyOrgListHint(
  firstPageStatus: number,
  headers: Record<string, string>,
): string | null {
  if (firstPageStatus !== 200) {
    return `GitHub returned HTTP ${firstPageStatus} with no organizations. Check the token, app installation, and GitHub App permissions.`;
  }

  const scopes = parseOAuthScopes(headers["x-oauth-scopes"]);

  if (scopes.length > 0 && !canListOrgsWithClassicScopes(scopes)) {
    return `Your token’s OAuth scopes (${scopes.join(", ")}) do not include user or read:org, which GitHub documents as required to list organizations for classic OAuth tokens. Re-authorize the app with the needed scopes.`;
  }

  if (scopes.length === 0) {
    return (
      "GitHub returned an empty list (HTTP 200). For GitHub App user tokens, GET /user/orgs usually reflects organizations this app can already work with—so the list can be empty until the app is installed somewhere, or when GitHub omits OAuth scope headers. " +
      "That does not mean the product is blocked: many apps pair this with GET /user/installations and an Install / Configure link so an org admin adds the app when they choose an organization. " +
      "In GitHub App settings, grant the minimum Organization permissions your API calls need (Metadata read-only is a typical baseline); add Members, Administration, Actions, Secrets, and other repository or organization permissions per endpoint—see GitHub’s documentation topic “Permissions required for GitHub Apps.”"
    );
  }

  return null;
}
