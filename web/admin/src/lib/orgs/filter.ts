export type OrgRow = {
  login: string;
  /**
   * True when we have evidence the user can push to at least one repo in this org.
   * Rows from GitHub App installations default to true (the app is installed on the org).
   */
  hasWritePathInOrg?: boolean;
  /**
   * From `GET /user/memberships/orgs` when available; filled after the org list loads.
   */
  membershipCanCreateRepository?: boolean | null;
};

/**
 * Case-insensitive substring search over org logins, then alphabetical sort.
 */
export function filterOrgsBySearch(orgs: OrgRow[], q: string): OrgRow[] {
  const p = q.trim().toLowerCase();
  const sorted = [...orgs].sort((a, b) => a.login.localeCompare(b.login));
  if (!p) return sorted;
  return sorted.filter((o) => o.login.toLowerCase().includes(p));
}
