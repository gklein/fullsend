import type { LayerReport } from "../status/types";
import { rollupOrgLayerStatus } from "../status/engine";
import { analyzeConfigRepoLayer } from "./configRepo";
import { analyzeDispatchTokenLayer } from "./dispatch";
import { analyzeEnrollmentLayer } from "./enrollment";
import type { LayerGithub } from "./githubClient";
import { analyzeSecretsLayer } from "./secrets";
import { analyzeWorkflowsLayer } from "./workflows";

export type AnalyzeOrgLayersInput = {
  org: string;
  gh: LayerGithub;
  /** Agent roles from org config (drives secret/variable names). */
  agents: { role: string }[];
  /** Repos with `enabled: true` in config (drives enrollment checks). */
  enabledRepos: string[];
};

/**
 * Runs read-only Analyze for all stack layers (order matches typical install stack).
 */
export async function analyzeOrgLayers(input: AnalyzeOrgLayersInput): Promise<{
  reports: LayerReport[];
  rollup: ReturnType<typeof rollupOrgLayerStatus>;
}> {
  const { org, gh, agents, enabledRepos } = input;
  const reports: LayerReport[] = [
    await analyzeConfigRepoLayer(org, gh),
    await analyzeWorkflowsLayer(org, gh),
    await analyzeSecretsLayer(org, gh, agents),
    await analyzeEnrollmentLayer(org, gh, enabledRepos),
    await analyzeDispatchTokenLayer(org, gh),
  ];
  return { reports, rollup: rollupOrgLayerStatus(reports) };
}
