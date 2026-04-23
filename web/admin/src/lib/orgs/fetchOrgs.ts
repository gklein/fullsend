import { createUserOctokit } from "../github/client";
import { buildEmptyOrgsFromReposHint, headersToRecord } from "./emptyOrgListHint";
import type { OrgRow } from "./filter";

/** Smaller GitHub pages so the UI can paint orgs sooner than 100-repo chunks. */
const REPOS_PER_PAGE = 25;

/** Cap pages when paginating GET /user/repos. */
const MAX_REPO_LIST_PAGES = 50;

export type FetchOrgsResult = {
  orgs: OrgRow[];
  /**
   * When `orgs` is empty, explains likely causes from the repository scan
   * (`GET /user/repos`).
   */
  emptyHint: string | null;
};

export type FetchOrgsProgressMeta = {
  done: boolean;
  /** Number of GitHub repo list pages processed so far. */
  repoPagesFetched: number;
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

function friendlyReposListHttpError(status: number, githubMessage: string): string {
  if (status === 403) {
    return "GitHub refused to list repositories (403). For classic OAuth, you may need the repo scope for private repositories. For a GitHub App, check repository permissions on the installation.";
  }
  if (status === 401) {
    return "Could not load repositories — sign in again if your token expired.";
  }
  return githubMessage;
}

function isOrganizationOwner(owner: unknown): owner is { login: string } {
  if (!owner || typeof owner !== "object") return false;
  const o = owner as Record<string, unknown>;
  return (
    o.type === "Organization" &&
    typeof o.login === "string" &&
    o.login.trim() !== ""
  );
}

function sortedOrgRowsFromMap(orgLogins: Map<string, string>): OrgRow[] {
  return [...orgLogins.values()]
    .sort((a, b) => a.localeCompare(b))
    .map((login) => ({ login }));
}

/**
 * Scans **`GET /user/repos`** page-by-page (`per_page` {@link REPOS_PER_PAGE}), calling
 * `onProgress` after each page with the cumulative unique organisation list so the UI can
 * paint early.
 */
export async function fetchOrgsWithProgress(
  accessToken: string,
  options: {
    force?: boolean;
    signal?: AbortSignal;
    onProgress: (orgs: OrgRow[], meta: FetchOrgsProgressMeta) => void;
  },
): Promise<FetchOrgsResult> {
  if (!options.force && memoryCache?.token === accessToken) {
    const { orgs, emptyHint } = memoryCache;
    options.onProgress(orgs, { done: true, repoPagesFetched: 0 });
    return { orgs, emptyHint };
  }

  const octokit = createUserOctokit(accessToken);

  try {
    const iterator = octokit.paginate.iterator(
      octokit.rest.repos.listForAuthenticatedUser,
      {
        per_page: REPOS_PER_PAGE,
        affiliation: "owner,collaborator,organization_member",
        sort: "full_name",
      },
    );

    let firstStatus = 200;
    let firstHeaders: Record<string, string> = {};
    const orgLogins = new Map<string, string>();
    let repoTotal = 0;
    let pages = 0;

    for await (const page of iterator) {
      if (options.signal?.aborted) {
        throw new DOMException("Aborted", "AbortError");
      }
      pages += 1;
      if (pages > MAX_REPO_LIST_PAGES) break;

      if (Object.keys(firstHeaders).length === 0) {
        firstStatus = page.status;
        firstHeaders = headersToRecord(page.headers);
      }

      const chunk = page.data;
      if (!Array.isArray(chunk)) continue;

      for (const repo of chunk) {
        repoTotal += 1;
        const owner = (repo as { owner?: unknown }).owner;
        if (isOrganizationOwner(owner)) {
          const login = owner.login.trim();
          orgLogins.set(login.toLowerCase(), login);
        }
      }

      const orgs = sortedOrgRowsFromMap(orgLogins);
      options.onProgress(orgs, { done: false, repoPagesFetched: pages });
    }

    const orgs = sortedOrgRowsFromMap(orgLogins);
    const emptyHint =
      orgs.length === 0
        ? buildEmptyOrgsFromReposHint(
            repoTotal,
            orgLogins.size,
            firstStatus,
            firstHeaders,
          )
        : null;

    memoryCache = { token: accessToken, orgs, emptyHint };
    options.onProgress(orgs, { done: true, repoPagesFetched: pages });
    return { orgs, emptyHint };
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      throw e;
    }
    const status = octokitErrorStatus(e);
    const msg = e instanceof Error ? e.message : "GitHub repository listing failed.";
    throw new FetchOrgsError(status, friendlyReposListHttpError(status, msg));
  }
}

/**
 * Builds the org picker from **`GET /user/repos`**: unique GitHub **Organization** owners among
 * repositories the user may access (owner, collaborator, organization_member).
 */
export async function fetchOrgs(
  accessToken: string,
  options?: { force?: boolean; signal?: AbortSignal },
): Promise<FetchOrgsResult> {
  return fetchOrgsWithProgress(accessToken, {
    force: options?.force,
    signal: options?.signal,
    onProgress: () => {},
  });
}
