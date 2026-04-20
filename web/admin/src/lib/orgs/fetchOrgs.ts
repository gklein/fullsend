import { createUserOctokit } from "../github/client";
import { buildEmptyOrgListHint, headersToRecord } from "./emptyOrgListHint";
import type { OrgRow } from "./filter";

export type FetchOrgsResult = {
  orgs: OrgRow[];
  /**
   * When `orgs` is empty, explains likely permission or token-class behavior
   * (GitHub often returns 200 + [] instead of 403 for fine-grained / app tokens).
   */
  emptyHint: string | null;
};

let memoryCache: {
  token: string;
  orgs: OrgRow[];
  emptyHint: string | null;
} | null = null;

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

function friendlyOrgListHttpError(status: number, githubMessage: string): string {
  if (status === 403) {
    return "GitHub refused to list organizations (403). Classic OAuth tokens need the user or read:org scope. For a GitHub App, check Organization permissions and that the app is installed on each organization you expect.";
  }
  if (status === 401) {
    return "Could not load organizations — sign in again if your token expired.";
  }
  return githubMessage;
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
): Promise<FetchOrgsResult> {
  if (!options?.force && memoryCache?.token === accessToken) {
    return {
      orgs: memoryCache.orgs,
      emptyHint: memoryCache.emptyHint,
    };
  }

  const octokit = createUserOctokit(accessToken);

  try {
    const iterator = octokit.paginate.iterator(
      octokit.rest.orgs.listForAuthenticatedUser,
      { per_page: 100 },
    );

    let firstStatus = 200;
    let firstHeaders: Record<string, string> = {};
    const logins = new Map<string, string>();

    for await (const page of iterator) {
      if (Object.keys(firstHeaders).length === 0) {
        firstStatus = page.status;
        firstHeaders = headersToRecord(page.headers);
      }
      const chunk = page.data;
      if (!Array.isArray(chunk)) continue;
      for (const org of chunk) {
        const login =
          typeof org.login === "string" ? org.login.trim() : "";
        if (login) logins.set(login.toLowerCase(), login);
      }
    }

    const orgs = [...logins.values()]
      .sort((a, b) => a.localeCompare(b))
      .map((login) => ({ login }));

    const emptyHint =
      orgs.length === 0
        ? buildEmptyOrgListHint(firstStatus, firstHeaders)
        : null;

    memoryCache = { token: accessToken, orgs, emptyHint };
    return { orgs, emptyHint };
  } catch (e) {
    const status = octokitErrorStatus(e);
    const msg = e instanceof Error ? e.message : "GitHub organizations failed.";
    throw new FetchOrgsError(
      status,
      friendlyOrgListHttpError(status, msg),
    );
  }
}
