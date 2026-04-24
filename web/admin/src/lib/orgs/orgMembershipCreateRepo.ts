import type { Octokit } from "@octokit/rest";

/**
 * Maps one `org-membership` payload from `GET /user/memberships/orgs` (or per-org variant)
 * to a ternary signal. GitHub may omit `permissions`; see GitHub REST `org-membership` schema.
 * @see https://docs.github.com/rest/orgs/members#list-organization-memberships-for-the-authenticated-user
 */
export function membershipCreateRepoSignalFromPayload(m: {
  role?: string;
  permissions?: { can_create_repository?: boolean };
}): boolean | null {
  const explicit = m.permissions?.can_create_repository;
  if (typeof explicit === "boolean") return explicit;
  if (m.role === "admin") return true;
  if (m.role === "billing_manager") return false;
  return null;
}

/**
 * One paginated `GET /user/memberships/orgs` call (active memberships) to learn
 * `permissions.can_create_repository` per organisation — the supported alternative
 * to a non-existent “dry create repo” API.
 */
export async function fetchMembershipCreateRepoSignals(
  octokit: Octokit,
  options?: { signal?: AbortSignal },
): Promise<Map<string, boolean | null>> {
  const map = new Map<string, boolean | null>();
  try {
    const rows = await octokit.paginate(octokit.rest.orgs.listMembershipsForAuthenticatedUser, {
      state: "active",
      per_page: 100,
      request: options?.signal ? { signal: options.signal } : undefined,
    });
    for (const m of rows) {
      const login = (m as { organization?: { login?: string } }).organization?.login?.trim();
      if (!login) continue;
      const key = login.toLowerCase();
      map.set(key, membershipCreateRepoSignalFromPayload(m as { role?: string; permissions?: { can_create_repository?: boolean } }));
    }
  } catch {
    /* leave empty → callers treat as unknown (null) for every org */
  }
  return map;
}
