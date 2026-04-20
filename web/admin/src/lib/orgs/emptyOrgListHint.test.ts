import { describe, it, expect } from "vitest";
import { buildEmptyOrgListHint } from "./emptyOrgListHint";

describe("buildEmptyOrgListHint", () => {
  it("returns GitHub App / empty-scope guidance when scopes header is absent on 200", () => {
    const h = buildEmptyOrgListHint(200, {});
    expect(h).toMatch(/HTTP 200/);
    expect(h).toMatch(/GET \/user\/installations/);
    expect(h).toMatch(/Metadata/);
  });

  it("returns classic OAuth scope guidance when scopes omit user/read:org", () => {
    const h = buildEmptyOrgListHint(200, { "x-oauth-scopes": "repo" });
    expect(h).toMatch(/read:org/);
    expect(h).toMatch(/repo/);
  });

  it("returns null when read:org is present", () => {
    expect(
      buildEmptyOrgListHint(200, { "x-oauth-scopes": "repo, read:org" }),
    ).toBeNull();
  });

  it("returns null when user scope is present", () => {
    expect(buildEmptyOrgListHint(200, { "x-oauth-scopes": "user" })).toBeNull();
  });

  it("mentions non-200 status", () => {
    const h = buildEmptyOrgListHint(502, {});
    expect(h).toMatch(/502/);
  });
});
