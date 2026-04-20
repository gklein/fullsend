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
      "GitHub returned no organizations with HTTP 200. Fine-grained personal access tokens and many GitHub App user tokens do that when organization data is not accessible (GitHub documents empty lists instead of 403). " +
      "Confirm this GitHub App is installed on each organization you expect and has Organization or repository permissions that allow org listing for your account."
    );
  }

  return null;
}
