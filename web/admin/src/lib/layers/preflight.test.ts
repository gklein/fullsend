import { describe, expect, it } from "vitest";
import {
  computePreflight,
  parseXOauthScopesHeader,
  preflightOk,
} from "./preflight";

describe("parseXOauthScopesHeader", () => {
  it("returns null for empty or missing", () => {
    expect(parseXOauthScopesHeader(undefined)).toBeNull();
    expect(parseXOauthScopesHeader("")).toBeNull();
    expect(parseXOauthScopesHeader("   ")).toBeNull();
  });

  it("splits comma-separated scopes", () => {
    expect(parseXOauthScopesHeader("repo, workflow")).toEqual(["repo", "workflow"]);
  });
});

describe("computePreflight", () => {
  it("marks skipped when granted unknown", () => {
    const r = computePreflight(["repo", "admin:org"], null);
    expect(r.skipped).toBe(true);
    expect(r.missing).toEqual([]);
    expect(preflightOk(r)).toBe(true);
  });

  it("lists missing scopes", () => {
    const r = computePreflight(["repo", "workflow"], ["repo"]);
    expect(r.skipped).toBe(false);
    expect(r.missing).toEqual(["workflow"]);
    expect(preflightOk(r)).toBe(false);
  });

  it("ok when all present", () => {
    const r = computePreflight(["repo"], ["repo", "read:org"]);
    expect(preflightOk(r)).toBe(true);
  });
});
