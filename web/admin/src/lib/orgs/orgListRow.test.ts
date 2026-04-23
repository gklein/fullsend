import { describe, expect, it } from "vitest";
import type { LayerReport } from "../status/types";
import {
  orgListRowFromAnalysis,
  type OrgListAnalysisErr,
  type OrgListAnalysisOk,
} from "./orgListRow";

function rep(
  name: string,
  status: LayerReport["status"],
): LayerReport {
  return {
    name,
    status,
    details: [],
    wouldInstall: [],
    wouldFix: [],
  };
}

describe("orgListRowFromAnalysis", () => {
  it("cannot_deploy on forbidden error", () => {
    const err: OrgListAnalysisErr = {
      kind: "error",
      message: "no access",
      forbidden: true,
    };
    expect(orgListRowFromAnalysis(err)).toEqual({
      kind: "cannot_deploy",
      reason: "no access",
    });
  });

  it("error on non-forbidden failure", () => {
    const err: OrgListAnalysisErr = {
      kind: "error",
      message: "network",
      forbidden: false,
    };
    expect(orgListRowFromAnalysis(err)).toEqual({
      kind: "error",
      message: "network",
    });
  });

  it("deploy when config repo not installed", () => {
    const ok: OrgListAnalysisOk = {
      kind: "ok",
      rollup: "not_installed",
      reports: [
        rep("config-repo", "not_installed"),
        rep("workflows", "not_installed"),
        rep("secrets", "not_installed"),
        rep("enrollment", "installed"),
        rep("dispatch-token", "not_installed"),
      ],
    };
    expect(orgListRowFromAnalysis(ok)).toEqual({ kind: "deploy" });
  });

  it("configure when config repo exists (installed)", () => {
    const ok: OrgListAnalysisOk = {
      kind: "ok",
      rollup: "degraded",
      reports: [
        rep("config-repo", "installed"),
        rep("workflows", "degraded"),
        rep("secrets", "not_installed"),
        rep("enrollment", "installed"),
        rep("dispatch-token", "not_installed"),
      ],
    };
    expect(orgListRowFromAnalysis(ok)).toEqual({ kind: "configure" });
  });

  it("configure when config repo degraded", () => {
    const ok: OrgListAnalysisOk = {
      kind: "ok",
      rollup: "degraded",
      reports: [
        rep("config-repo", "degraded"),
        rep("workflows", "not_installed"),
        rep("secrets", "not_installed"),
        rep("enrollment", "installed"),
        rep("dispatch-token", "not_installed"),
      ],
    };
    expect(orgListRowFromAnalysis(ok)).toEqual({ kind: "configure" });
  });
});
