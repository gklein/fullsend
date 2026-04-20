import { createUserOctokit } from "../github/client";
import type { OrgRow } from "./filter";

let memoryCache: { token: string; orgs: OrgRow[] } | null = null;

/** Clears the in-memory org list cache (call on sign-out or when switching accounts). */
export function clearOrgListMemoryCache(): void {
  memoryCache = null;
}

export class FetchOrgsError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "FetchOrgsError";
    this.status = status;
  }
}

function octokitErrorStatus(e: unknown): number {
  if (
    typeof e === "object" &&
    e !== null &&
    "status" in e &&
    typeof (e as { status: unknown }).status === "number"
  ) {
    return (e as { status: number }).status;
  }
  return 502;
}

/**
 * Lists organizations the token may access via `GET /user/orgs` in the browser (CORS allows it).
 *
 * We use {@link Octokit.rest.orgs.listForAuthenticatedUser} rather than
 * `listMembershipsForAuthenticatedUser` (`GET /user/memberships/orgs`): GitHub App user access
 * tokens commonly return an **empty** memberships list while `/user/orgs` still reflects orgs
 * the app installation can see (see GitHub REST docs for both endpoints).
 */
export async function fetchOrgs(
  accessToken: string,
  options?: { force?: boolean },
): Promise<OrgRow[]> {
  if (!options?.force && memoryCache?.token === accessToken) {
    return memoryCache.orgs;
  }

  const octokit = createUserOctokit(accessToken);

  let orgPayloads: { login?: string }[];

  try {
    orgPayloads = await octokit.paginate(
      octokit.rest.orgs.listForAuthenticatedUser,
      { per_page: 100 },
    );
  } catch (e) {
    const status = octokitErrorStatus(e);
    const msg = e instanceof Error ? e.message : "GitHub organizations failed.";
    throw new FetchOrgsError(status, msg);
  }

  const logins = new Map<string, string>();
  for (const org of orgPayloads) {
    const login =
      typeof org.login === "string" ? org.login.trim() : "";
    if (login) logins.set(login.toLowerCase(), login);
  }

  const orgs = [...logins.values()]
    .sort((a, b) => a.localeCompare(b))
    .map((login) => ({ login }));

  memoryCache = { token: accessToken, orgs };
  return orgs;
}
