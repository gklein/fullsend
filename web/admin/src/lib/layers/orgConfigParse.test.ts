import { describe, expect, it } from "vitest";
import {
  agentsFromConfig,
  enabledReposFromConfig,
  parseOrgConfigYaml,
  validateOrgConfig,
} from "./orgConfigParse";

describe("validateOrgConfig", () => {
  it("accepts minimal valid config", () => {
    const cfg = parseOrgConfigYaml(`version: "1"
dispatch:
  platform: github-actions
defaults:
  roles: [fullsend]
  max_implementation_retries: 0
repos: {}
`);
    expect(validateOrgConfig(cfg)).toBeNull();
  });

  it("rejects bad version", () => {
    expect(
      validateOrgConfig(
        parseOrgConfigYaml(`version: "9"
dispatch:
  platform: github-actions
`),
      ),
    ).toContain("unsupported version");
  });

  it("lists agents and enabled repos from config", () => {
    const cfg = parseOrgConfigYaml(`version: "1"
dispatch:
  platform: github-actions
defaults:
  roles: [fullsend]
agents:
  - role: triage
    slug: t
repos:
  zed:
    enabled: false
  alpha:
    enabled: true
  beta:
    enabled: true
`);
    expect(validateOrgConfig(cfg)).toBeNull();
    expect(agentsFromConfig(cfg)).toEqual([{ role: "triage" }]);
    expect(enabledReposFromConfig(cfg)).toEqual(["alpha", "beta"]);
  });
});
