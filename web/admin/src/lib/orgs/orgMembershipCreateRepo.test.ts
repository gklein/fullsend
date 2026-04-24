import { describe, expect, it } from "vitest";
import { membershipCreateRepoSignalFromPayload } from "./orgMembershipCreateRepo";

describe("membershipCreateRepoSignalFromPayload", () => {
  it("uses permissions.can_create_repository when present", () => {
    expect(
      membershipCreateRepoSignalFromPayload({
        role: "member",
        permissions: { can_create_repository: false },
      }),
    ).toBe(false);
    expect(
      membershipCreateRepoSignalFromPayload({
        role: "member",
        permissions: { can_create_repository: true },
      }),
    ).toBe(true);
  });

  it("treats org admin role as able to create repositories", () => {
    expect(membershipCreateRepoSignalFromPayload({ role: "admin" })).toBe(true);
  });

  it("treats billing_manager as not a repo-creation path", () => {
    expect(membershipCreateRepoSignalFromPayload({ role: "billing_manager" })).toBe(false);
  });

  it("returns null for plain member without permissions object", () => {
    expect(membershipCreateRepoSignalFromPayload({ role: "member" })).toBeNull();
  });
});
