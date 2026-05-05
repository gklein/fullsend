import { afterEach, describe, expect, it } from "vitest";
import { persistExpandedPathInSession } from "./treeSession";

describe("persistExpandedPathInSession", () => {
  afterEach(() => {
    sessionStorage.clear();
  });

  it("sets expanded flag for each path prefix", () => {
    persistExpandedPathInSession("problems/applied");
    expect(sessionStorage.getItem("fullsend-docs-tree:problems")).toBe("1");
    expect(sessionStorage.getItem("fullsend-docs-tree:problems/applied")).toBe(
      "1",
    );
  });
});
