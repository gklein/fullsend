import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../github/client", () => ({
  createUserOctokit: vi.fn(),
}));

import { createUserOctokit } from "../github/client";
import {
  clearOrgListMemoryCache,
  fetchOrgs,
  fetchOrgsWithProgress,
  FetchOrgsError,
} from "./fetchOrgs";

function mockOctokit(iterator: () => AsyncIterableIterator<unknown>) {
  vi.mocked(createUserOctokit).mockReturnValue({
    paginate: {
      iterator: vi.fn(iterator),
    },
    rest: {
      repos: {
        listForAuthenticatedUser: {},
      },
    },
  } as never);
}

describe("fetchOrgs", () => {
  beforeEach(() => {
    clearOrgListMemoryCache();
    vi.mocked(createUserOctokit).mockReset();
  });

  it("returns unique organisation owners sorted by login", async () => {
    mockOctokit(() =>
      (async function* () {
        yield {
          status: 200,
          headers: new Headers(),
          data: [
            { owner: { type: "Organization", login: "zebra" } },
            { owner: { type: "Organization", login: "alpha" } },
            { owner: { type: "Organization", login: "alpha" } },
          ],
        };
      })(),
    );

    const r = await fetchOrgs("token", { force: true });
    expect(r.orgs.map((o) => o.login)).toEqual(["alpha", "zebra"]);
    expect(r.emptyHint).toBeNull();
  });

  it("returns empty orgs with emptyHint when no organisation-owned repos", async () => {
    mockOctokit(() =>
      (async function* () {
        yield {
          status: 200,
          headers: new Headers({ "x-oauth-scopes": "repo" }),
          data: [{ owner: { type: "User", login: "someone" } }],
        };
      })(),
    );

    const r = await fetchOrgs("token", { force: true });
    expect(r.orgs).toEqual([]);
    expect(r.emptyHint).toBeTruthy();
    expect(typeof r.emptyHint).toBe("string");
  });

  it("throws FetchOrgsError for non-401 HTTP failures", async () => {
    mockOctokit(() => ({
      [Symbol.asyncIterator]: () => ({
        next: () =>
          Promise.reject(Object.assign(new Error("Forbidden"), { status: 403 })),
      }),
    }));

    try {
      await fetchOrgs("token", { force: true });
      expect.fail("expected rejection");
    } catch (e) {
      expect(e).toBeInstanceOf(FetchOrgsError);
      expect((e as FetchOrgsError).status).toBe(403);
      expect((e as FetchOrgsError).message).toContain("403");
    }
  });

  it("throws AbortError when signal is already aborted", async () => {
    const ac = new AbortController();
    ac.abort();

    mockOctokit(() =>
      (async function* () {
        yield { status: 200, headers: new Headers(), data: [] };
      })(),
    );

    await expect(
      fetchOrgs("token", { force: true, signal: ac.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });
  });

  it("throws AbortError when aborted from onProgress mid-scan", async () => {
    const ac = new AbortController();

    mockOctokit(() =>
      (async function* () {
        yield { status: 200, headers: new Headers(), data: [] };
        await Promise.resolve();
        yield {
          status: 200,
          headers: new Headers(),
          data: [{ owner: { type: "Organization", login: "LateOrg" } }],
        };
      })(),
    );

    await expect(
      fetchOrgsWithProgress("token", {
        force: true,
        signal: ac.signal,
        onProgress: () => {
          ac.abort();
        },
      }),
    ).rejects.toMatchObject({ name: "AbortError" });
  });
});
