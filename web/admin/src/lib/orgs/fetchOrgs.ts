import { notifyGitHubUserUnauthorized } from "../auth/githubUnauthorized";
import { createUserOctokit } from "../github/client";
import { buildEmptyInstallationsHint } from "./emptyOrgListHint";
import type { OrgRow } from "./filter";
import {
  orgRowsAndSlugFromInstallations,
  type MinimalInstallation,
} from "./installationOrgRows";

export const INSTALLATIONS_PER_PAGE = 30;

/** Cap pages when paginating `GET /user/installations`. */
export const MAX_INSTALLATION_LIST_PAGES = 20;

export type FetchOrgsResult = {
  orgs: OrgRow[];
  /**
   * When `orgs` is empty after a **successful** installations scan, explains that no org
   * installs were found (not HTTP error text).
   */
  emptyHint: string | null;
  /** First app slug from installation payloads in page order, if any. */
  appSlugFromApi: string | null;
  /**
   * True when pagination stopped at {@link MAX_INSTALLATION_LIST_PAGES} because GitHub may
   * have more installation pages (list may be incomplete).
   */
  installationListTruncated: boolean;
};

export type FetchOrgsProgressMeta = {
  done: boolean;
  /** Number of GitHub installation list pages processed so far. */
  installationPagesFetched: number;
};

let memoryCache: {
  token: string;
  orgs: OrgRow[];
  emptyHint: string | null;
  appSlugFromApi: string | null;
  installationListTruncated: boolean;
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

function friendlyInstallationsListHttpError(
  status: number,
  githubMessage: string,
): string {
  if (status === 403) {
    return (
      "GitHub refused to list app installations (403). " +
      "The Fullsend Admin app may need additional permissions, or your account cannot access installations. " +
      "If you operate this deployment, check the app’s settings; otherwise ask an org admin to install the app."
    );
  }
  if (status === 401) {
    return "Could not list installations — sign in again if your token expired.";
  }
  return githubMessage;
}

function installationsFromPageData(data: unknown): MinimalInstallation[] {
  // Octokit’s paginate iterator may expose either `{ installations: [...] }` or a bare array.
  if (Array.isArray(data)) return data as MinimalInstallation[];
  if (!data || typeof data !== "object") return [];
  const rec = data as Record<string, unknown>;
  const raw = rec.installations;
  if (!Array.isArray(raw)) return [];
  return raw as MinimalInstallation[];
}

/**
 * Paginates **`GET /user/installations`**, calling `onProgress` after each page with the
 * cumulative organisation list derived from **Organization** installations.
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
    if (options.signal?.aborted) {
      throw new DOMException("Aborted", "AbortError");
    }
    const { orgs, emptyHint, appSlugFromApi, installationListTruncated } =
      memoryCache;
    options.onProgress(orgs, { done: true, installationPagesFetched: 0 });
    return {
      orgs,
      emptyHint,
      appSlugFromApi,
      installationListTruncated,
    };
  }

  const octokit = createUserOctokit(accessToken);

  try {
    if (options.signal?.aborted) {
      throw new DOMException("Aborted", "AbortError");
    }

    const iterator = octokit.paginate.iterator(
      octokit.rest.apps.listInstallationsForAuthenticatedUser,
      { per_page: INSTALLATIONS_PER_PAGE },
    );

    const accumulated: MinimalInstallation[] = [];
    let pages = 0;

    for await (const page of iterator) {
      if (options.signal?.aborted) {
        throw new DOMException("Aborted", "AbortError");
      }
      pages += 1;
      if (pages > MAX_INSTALLATION_LIST_PAGES) break;

      accumulated.push(...installationsFromPageData(page.data));

      const { orgs, appSlug } = orgRowsAndSlugFromInstallations(accumulated);
      options.onProgress(orgs, {
        done: false,
        installationPagesFetched: pages,
      });
    }

    const { orgs, appSlug } = orgRowsAndSlugFromInstallations(accumulated);
    const emptyHint = orgs.length === 0 ? buildEmptyInstallationsHint() : null;
    const installationListTruncated = pages > MAX_INSTALLATION_LIST_PAGES;

    memoryCache = {
      token: accessToken,
      orgs,
      emptyHint,
      appSlugFromApi: appSlug,
      installationListTruncated,
    };
    options.onProgress(orgs, { done: true, installationPagesFetched: pages });
    return {
      orgs,
      emptyHint,
      appSlugFromApi: appSlug,
      installationListTruncated,
    };
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      throw e;
    }
    const status = octokitErrorStatus(e);
    if (status === 401) {
      notifyGitHubUserUnauthorized();
    }
    const msg = e instanceof Error ? e.message : "GitHub installation listing failed.";
    throw new FetchOrgsError(status, friendlyInstallationsListHttpError(status, msg));
  }
}

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
