import { describe, expect, it } from "vitest";
import { formatDocHash, parseDocHash } from "./hashRoute";

describe("parseDocHash", () => {
  it("parses route only", () => {
    expect(parseDocHash("#/guides/admin/installation")).toEqual({
      routeKey: "guides/admin/installation",
    });
  });

  it("parses route and slug on first :: only", () => {
    expect(parseDocHash("#/a/b::my-slug")).toEqual({
      routeKey: "a/b",
      slug: "my-slug",
    });
  });

  it("returns null for default", () => {
    expect(parseDocHash("")).toBeNull();
    expect(parseDocHash("#/")).toBeNull();
  });
});

describe("formatDocHash", () => {
  it("round-trips", () => {
    const k = "guides/admin/installation";
    expect(parseDocHash(formatDocHash(k))).toEqual({ routeKey: k });
    expect(parseDocHash(formatDocHash(k, "x"))).toEqual({
      routeKey: k,
      slug: "x",
    });
  });
});
