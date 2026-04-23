import type { Octokit } from "@octokit/rest";
import { RequestError } from "@octokit/request-error";
import { analyzeOrgLayers } from "../layers/analyzeOrg";
import { CONFIG_FILE_PATH, CONFIG_REPO_NAME } from "../layers/constants";
import { createLayerGithub } from "../layers/githubClient";
import {
  agentsFromConfig,
  enabledReposFromConfig,
  parseOrgConfigYaml,
  validateOrgConfig,
} from "../layers/orgConfigParse";
import type { LayerReport, LayerStatus } from "../status/types";

export type OrgListAnalysisOk = {
  kind: "ok";
  rollup: LayerStatus;
  reports: LayerReport[];
};

export type OrgListAnalysisErr = {
  kind: "error";
  message: string;
  /** True when GitHub returned 403 (token cannot read this org’s installation state). */
  forbidden: boolean;
};

/**
 * Runs the read-only layer stack for one org so the org list can show
 * Configure / Deploy / Cannot deploy (see UX spec — Organisation selection — list).
 */
export async function analyzeOrgForOrgList(
  org: string,
  octokit: Octokit,
): Promise<OrgListAnalysisOk | OrgListAnalysisErr> {
  const gh = createLayerGithub(octokit);
  try {
    const exists = await gh.getRepoExists(org, CONFIG_REPO_NAME);
    let agents: { role: string }[] = [];
    let enabledRepos: string[] = [];
    if (exists) {
      const raw = await gh.getRepoFileUtf8(org, CONFIG_REPO_NAME, CONFIG_FILE_PATH);
      if (raw) {
        try {
          const cfg = parseOrgConfigYaml(raw);
          if (validateOrgConfig(cfg) === null) {
            agents = agentsFromConfig(cfg);
            enabledRepos = enabledReposFromConfig(cfg);
          }
        } catch {
          /* invalid YAML — still analyze other layers with empty agents/repos */
        }
      }
    }
    const { reports, rollup } = await analyzeOrgLayers({
      org,
      gh,
      agents,
      enabledRepos,
    });
    return { kind: "ok", reports, rollup };
  } catch (e) {
    if (e instanceof RequestError && e.status === 403) {
      return {
        kind: "error",
        message:
          "Insufficient permissions to evaluate Fullsend state for this organisation.",
        forbidden: true,
      };
    }
    return {
      kind: "error",
      message: e instanceof Error ? e.message : String(e),
      forbidden: false,
    };
  }
}

export type OrgListRowCluster =
  | { kind: "checking" }
  | { kind: "configure" }
  | { kind: "deploy" }
  | { kind: "cannot_deploy"; reason: string }
  | { kind: "error"; message: string };

/**
 * Maps layer analysis to the mutually exclusive trailing cluster on the org list.
 */
export function orgListRowFromAnalysis(
  result: OrgListAnalysisOk | OrgListAnalysisErr,
): OrgListRowCluster {
  if (result.kind === "error") {
    if (result.forbidden) {
      return { kind: "cannot_deploy", reason: result.message };
    }
    return { kind: "error", message: result.message };
  }

  const configReport = result.reports.find((r: LayerReport) => r.name === "config-repo");
  if (!configReport) {
    return { kind: "error", message: "Missing config-repo layer report." };
  }

  if (configReport.status === "not_installed") {
    return { kind: "deploy" };
  }

  return { kind: "configure" };
}
