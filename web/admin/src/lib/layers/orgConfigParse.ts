import { parse } from "yaml";

/** Parsed shape of `config.yaml` (mirrors `internal/config/config.go`). */
export type OrgConfigYaml = {
  version?: string;
  dispatch?: { platform?: string };
  defaults?: {
    roles?: string[];
    max_implementation_retries?: number;
    auto_merge?: boolean;
  };
  agents?: { role: string; name?: string; slug?: string }[];
  repos?: Record<string, { enabled?: boolean; roles?: string[] }>;
};

const VALID_ROLES = new Set(["fullsend", "triage", "coder", "review"]);

export function parseOrgConfigYaml(data: string): OrgConfigYaml {
  const doc = parse(data) as unknown;
  if (doc === null || typeof doc !== "object" || Array.isArray(doc)) {
    throw new Error("parsing org config: root must be a mapping");
  }
  return doc as OrgConfigYaml;
}

/** @returns null if valid, otherwise a human-readable error string (matches Go Validate errors). */
export function validateOrgConfig(cfg: OrgConfigYaml): string | null {
  if (cfg.version !== "1") {
    return `unsupported version ${JSON.stringify(cfg.version)}: must be "1"`;
  }
  if (cfg.dispatch?.platform !== "github-actions") {
    return `unsupported platform ${JSON.stringify(cfg.dispatch?.platform)}: must be "github-actions"`;
  }
  const retries = cfg.defaults?.max_implementation_retries;
  if (typeof retries === "number" && retries < 0) {
    return `max_implementation_retries must be >= 0, got ${retries}`;
  }
  for (const role of cfg.defaults?.roles ?? []) {
    if (!VALID_ROLES.has(role)) {
      return `invalid role ${JSON.stringify(role)}: must be one of fullsend, triage, coder, review`;
    }
  }
  return null;
}

/** Agent rows for secrets-layer analyze (mirrors `config.OrgConfig.Agents`). */
export function agentsFromConfig(cfg: OrgConfigYaml): { role: string }[] {
  return (cfg.agents ?? []).map((a) => ({ role: a.role }));
}

/** Enabled repo names for enrollment-layer analyze (sorted). */
export function enabledReposFromConfig(cfg: OrgConfigYaml): string[] {
  const repos = cfg.repos ?? {};
  return Object.entries(repos)
    .filter(([, v]) => v?.enabled === true)
    .map(([name]) => name)
    .sort((a, b) => a.localeCompare(b));
}
