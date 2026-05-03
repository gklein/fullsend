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

/** 512 KiB — more than sufficient for any realistic org `config.yaml`. */
export const MAX_ORG_CONFIG_YAML_UTF8_BYTES = 512 * 1024;

/**
 * Maximum nesting depth of mappings and sequences after parse (mitigates YAML bombs).
 * Real configs are shallow; this is intentionally generous.
 */
export const MAX_ORG_CONFIG_YAML_DEPTH = 64;

/** Thrown when `config.yaml` exceeds size or structural depth limits (see parse helpers). */
export class OrgConfigYamlLimitError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "OrgConfigYamlLimitError";
  }
}

function utf8ByteLength(s: string): number {
  return new TextEncoder().encode(s).length;
}

/** Deepest path from `value` through nested objects/arrays (scalar leaves report their `depth`). */
function measureYamlTreeDepth(value: unknown, depth: number): number {
  if (value === null || typeof value !== "object") return depth;
  if (Array.isArray(value)) {
    let m = depth;
    for (const el of value) {
      m = Math.max(m, measureYamlTreeDepth(el, depth + 1));
    }
    return m;
  }
  let m = depth;
  for (const k of Object.keys(value as object)) {
    m = Math.max(
      m,
      measureYamlTreeDepth(
        (value as Record<string, unknown>)[k],
        depth + 1,
      ),
    );
  }
  return m;
}

export function parseOrgConfigYaml(data: string): OrgConfigYaml {
  const bytes = utf8ByteLength(data);
  if (bytes > MAX_ORG_CONFIG_YAML_UTF8_BYTES) {
    throw new OrgConfigYamlLimitError(
      `Organisation config YAML exceeds the maximum file size (limit ${MAX_ORG_CONFIG_YAML_UTF8_BYTES} bytes, 512 KiB). This file is ${bytes} bytes. Reduce the file size to continue.`,
    );
  }

  let doc: unknown;
  try {
    doc = parse(data) as unknown;
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    throw new Error(`parsing org config YAML: ${msg}`);
  }

  if (doc === null || typeof doc !== "object" || Array.isArray(doc)) {
    throw new Error("parsing org config: root must be a mapping");
  }

  const deepest = measureYamlTreeDepth(doc, 0);
  if (deepest > MAX_ORG_CONFIG_YAML_DEPTH) {
    throw new OrgConfigYamlLimitError(
      `Organisation config YAML is nested too deeply (depth ${deepest}, maximum ${MAX_ORG_CONFIG_YAML_DEPTH}). Simplify mapping and list nesting so the document stays within the limit.`,
    );
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
