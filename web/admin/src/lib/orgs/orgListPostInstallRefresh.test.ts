import { afterEach, describe, expect, it } from "vitest";
import {
  consumePendingOrgListRefresh,
  setPendingOrgListRefreshAfterInstall,
} from "./orgListPostInstallRefresh";

afterEach(() => sessionStorage.clear());

describe("orgListPostInstallRefresh", () => {
  it("consume returns false when not set", () => {
    expect(consumePendingOrgListRefresh()).toBe(false);
  });

  it("set then consume returns true once", () => {
    setPendingOrgListRefreshAfterInstall();
    expect(consumePendingOrgListRefresh()).toBe(true);
    expect(consumePendingOrgListRefresh()).toBe(false);
  });
});
