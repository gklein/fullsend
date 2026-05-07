---
title: "29. Universal harness access via URLs and paths"
status: Proposed
relates_to:
  - agent-architecture
  - security-threat-model
  - agent-infrastructure
topics:
  - harness
  - portability
  - security
  - remote-resources
---

# 29. Universal harness access via URLs and paths

Date: 2026-05-07

## Status

Proposed

## Context

Currently, harnesses reference local files through relative paths resolved against the `.fullsend` directory structure (ADR-0024). A harness configuration might reference:

```yaml
agent: agents/code.md
policy: policies/code.yaml
skills:
  - skills/code-implementation
pre_script: scripts/pre-code.sh
```

All these paths are resolved to absolute paths relative to the `.fullsend` directory base. This works well for organization-controlled harnesses living in the `.fullsend` repository, but creates several limitations:

1. **Harnesses cannot be shared externally.** A useful harness definition developed for one organization cannot easily be shared with another without copy-pasting the entire directory structure (harness YAML, agent definitions, skills, policies, scripts).

2. **Agents are not standalone artifacts.** An agent definition in `agents/code.md` cannot reference external skills, community-maintained policies, or third-party tools without those resources being copied into the local `.fullsend` structure first.

3. **Cross-repository composition is manual.** Organizations cannot maintain a library of reusable agent components (skills, policies) in a separate repository and reference them from multiple `.fullsend` repositories without manual synchronization.

4. **Upstream/downstream friction.** Downstream organizations using fullsend want to consume upstream-provided harnesses, agents, and skills while maintaining local customizations. The current path-only model forces a fork-and-modify approach rather than allowing selective overlay of specific resources.

5. **Runtime dependency discovery is static.** The harness declares all resources it needs upfront. Agents cannot discover and load additional skills, policies, or tools at runtime based on the specific problem they encounter.

### Why this matters

The goal is to make agents universally accessible: a harness should be invocable from anywhere, referencing resources from anywhere, without requiring a monolithic local copy of all dependencies. This enables:

- **Community sharing:** "Here's a harness for Rust linting" as a single URL, not a 6-file directory structure
- **Composability:** Mix org-provided agents with community skills and upstream policies
- **Decentralized evolution:** Skill authors can publish skills independently; agent authors can reference them by URL
- **Runtime adaptation:** Agents can discover what they need during execution (e.g., fetch a domain-specific skill when encountering unfamiliar code)

This is analogous to how modern programming ecosystems work: you don't copy `requests.py` into your repo, you declare `requests==2.31.0` and the package manager fetches it. Harnesses should be similarly composable.

### The transitive closure problem

If a harness can reference a skill by URL, and that skill references a policy file, the policy file must also support URL or path references. If the policy references a tool binary, the binary must be fetchable. This **transitive closure** property must apply uniformly: anything referenced by a harness component must itself be accessible via URL or path.

## Options

### Option A: URL support everywhere with local caching

Extend every path field in the harness schema to support three forms:

1. **Absolute file path:** `/opt/fullsend/agents/code.md`
2. **Relative file path:** `agents/code.md` (resolved against `.fullsend` base)
3. **HTTP(S) URL:** `https://github.com/fullsend-ai/library/agents/code.md`

When the runner encounters a URL, it fetches the resource, caches it locally (content-addressed by SHA256), and validates its integrity before use. All referenced resources (skills, policies, scripts, binaries) support the same three forms, creating a uniform resolution model.

**Transitive closure:** A URL-referenced skill that itself references `policy: https://example.com/policy.yaml` triggers a recursive fetch. The runner builds a complete dependency graph before sandbox creation.

**Trade-offs:**
- **Pros:** Maximum flexibility. Enables community sharing, decentralized libraries, mix-and-match composition. Harnesses become portable.
- **Cons:** Introduces TOCTOU (time-of-check-time-of-use) attacks, content injection via compromised URLs, dependency confusion, and a new attack surface (any URL the runner fetches becomes a potential injection point). Requires robust caching, integrity checking, and SSRF protection.

### Option B: URL support with explicit pinning

Like Option A, but all URLs must include an integrity hash:

```yaml
agent: https://github.com/fullsend-ai/library/agents/code.md#sha256=abc123...
```

The runner verifies the fetched content matches the declared hash before using it. This prevents TOCTOU attacks at the cost of requiring hash management for every remote resource.

**Trade-offs:**
- **Pros:** Eliminates silent substitution attacks. Makes dependency versions explicit.
- **Cons:** Hash management is manual and error-prone. Updating a remote resource requires updating every hash reference. No auto-update path (by design).

### Option C: URL support only for read-only resources

Allow URLs only for declarative resources (agent definitions, skills, policies) but not for executable resources (scripts, binaries). Scripts and binaries must be local files.

This reduces the attack surface: a compromised URL can deliver malicious agent instructions (mitigated by schema validation and output checking per ADR-0022) but cannot directly execute arbitrary code on the runner host.

**Trade-offs:**
- **Pros:** Limits blast radius. Scripts running on the host (pre/post) are always local and auditable.
- **Cons:** Still allows prompt injection via malicious agent definitions or skills. Partial solution that doesn't address the full composability problem.

### Option D: Local-only with explicit import tooling

Keep the current local-path-only model. Introduce a `fullsend import` command that fetches remote harnesses, agents, skills, and policies, writes them to the local `.fullsend` structure, and optionally pins versions in a lock file.

Harnesses remain local-only at runtime. Sharing and composition happen at development time, not runtime.

**Trade-offs:**
- **Pros:** No runtime network dependencies. All resources are local and auditable before use. Lock file model (like `package-lock.json`) provides version pinning and integrity checking.
- **Cons:** No runtime adaptation. Harnesses are not standalone—sharing requires sharing the import manifest. Defeats the goal of universal access.

### Option Z: No change (status quo)

All resources remain local paths. Sharing requires manual copy-paste.

**Trade-offs:**
- **Pros:** Simple. No new attack surface. Everything is auditable locally.
- **Cons:** Defeats the goal of universal harness access. Organizations cannot share or compose agents without manual duplication.

## Decision

**Status: Deferred** — pending security review and consensus on trust model.

This ADR is **not accepted**. The proposed approach described below is presented for discussion only. Do not implement until:
1. The implementation plan in `docs/plans/universal-harness-access.md` has been reviewed
2. Key security questions in the Open Questions section are resolved
3. Consensus is reached on the trust model for remote resources

### Proposed approach (pending security review)

**Option A with security extensions** is proposed for consideration:

- Support URLs, absolute paths, and relative paths uniformly for all harness resources
- Fetch and cache remote resources content-addressed by SHA256
- Validate integrity, apply SSRF protection, and enforce per-resource policies (read-only vs executable)
- Extend transitive closure to all referenced resources
- Introduce access policies that constrain what remote resources can do (more restrictive than local resources)

## Consequences

If Option A (URL support everywhere with security extensions) is accepted:

### What changes

- **Harness schema:** Every path field (`agent`, `policy`, `skills[]`, `host_files[].src`, `pre_script`, `post_script`, etc.) accepts URLs.
- **Resolution logic:** The runner resolves URLs by fetching, caching (content-addressed), and validating before use.
- **Transitive closure:** Referenced resources (skills, policies) are parsed to extract their own references, which are recursively fetched.
- **Access policies:** Runtime policies constrain what URL-referenced resources can do (e.g., URL-sourced scripts run with reduced privileges or not at all).

### Security implications (CRITICAL)

1. **TOCTOU (Time-of-Check-Time-of-Use):** A remote resource could change between fetch and use. **Mitigation:** Content-addressed caching. Once fetched and validated, the cached version is immutable. The cache key is `SHA256(URL + content)`.

2. **Content injection via compromised URLs:** An attacker who controls a URL referenced by a harness can inject malicious agent instructions, skills, or policies. **Mitigations:**
   - Schema validation (ADR-0022): All fetched resources are validated against their schema before use.
   - Output validation: Agent output is validated regardless of source.
   - SSRF protection: Runner applies URL allowlists (e.g., only `https://github.com`, `https://gitlab.com`, `https://cdn.fullsend.ai`).
   - Signature verification (future): Remote resources could be signed by their publisher, verified by the runner.

3. **Dependency confusion:** An attacker publishes a malicious skill at `https://attacker.com/skills/common-name` and tricks a harness into referencing it instead of the legitimate `https://fullsend.ai/skills/common-name`. **Mitigations:**
   - Explicit URL references (no auto-resolution of names to URLs).
   - URL allowlists per organization (configurable in `config.yaml`).
   - Lock files (future): Pin exact URLs and hashes for all transitive dependencies.

4. **Prompt injection via skills:** A URL-fetched skill contains adversarial instructions designed to manipulate the agent. **Mitigations:**
   - All skills (local or remote) pass through the same security scanners (unicode normalization, context injection detection, LLM Guard).
   - Remote skills are subject to more restrictive policies than local skills (e.g., cannot reference executable scripts).

5. **Executable code from URLs:** Pre/post scripts fetched from URLs run on the runner host with full privileges. **Mitigation:** Apply **Option C** restriction: scripts and binaries must be local files. Only declarative resources (agents, skills, policies, schemas) can be URLs. Or, URL-sourced scripts run in a restricted sandbox with no access to secrets.

6. **Runtime dependency discovery increases attack surface:** If agents can fetch resources at runtime based on dynamic input (e.g., "I need a Python linting skill for this repo"), an attacker can manipulate input to trigger fetch of a malicious resource. **Mitigations:**
   - Runtime resource loading is opt-in per harness (disabled by default).
   - All runtime-fetched resources go through the same validation and caching.
   - Audit log of all fetched URLs per agent run.

7. **SSRF (Server-Side Request Forgery):** The runner's URL fetch mechanism could be exploited to probe internal networks or exfiltrate data via DNS. **Mitigations:**
   - URL allowlists (only permit known-good domains).
   - No URL redirects (HTTP 3xx responses are rejected).
   - No internal IPs (reject `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `fc00::/7`).
   - No non-HTTPS URLs (reject `http://`, `ftp://`, `file://`).

### Access policy design

The implementation must address **how access policies work when agents don't know what they need until runtime.** Proposed model:

- **Static resource declaration:** The harness declares allowed URL prefixes (e.g., `allowed_remote_resources: ["https://github.com/fullsend-ai/library/"]`).
- **Runtime fetch is constrained by declaration:** The agent can fetch any URL matching an allowed prefix. Fetches outside allowed prefixes are blocked.
- **Audit and alert:** All runtime fetches are logged. Anomalous fetch patterns (e.g., sudden fetches from a new domain) trigger alerts.

### Changes required

See `docs/plans/universal-harness-access.md` for detailed implementation plan. Key changes:

1. **Harness loader (`internal/harness/harness.go`):** Add URL resolution and caching logic.
2. **Resource fetcher (new package `internal/fetch/`):** HTTP client with SSRF protection, caching, integrity checking.
3. **Transitive resolver (new package `internal/resolve/`):** Build dependency graph for harnesses, recursively fetch and validate.
4. **Access policy enforcement (`internal/security/`):** Validate fetched resources against org-level and harness-level policies.
5. **Schema extension:** Add `allowed_remote_resources[]` to harness YAML.
6. **CLI flag:** `fullsend run --offline` to disable all network fetches (fail if harness references a URL).

### Differences from traditional package management

This approach differs from traditional package management systems (npm, pip, cargo) in important ways:

- **Composable files, not blackbox packages:** Harnesses are not packaged as opaque bundles. Instead, they reference individual files (agent definitions, skills, policies) that can live in different locations. A harness might reference an agent from one repository, skills from another, and a policy from a third. This is more flexible and encourages fine-grained reuse — you can mix-and-match components without forking entire packages.

- **Trade-offs of granular composition:**
  - **Pros:** Encourages modular design and selective reuse. Organizations can adopt upstream agents while providing their own policies, or use community skills with organization-controlled agent definitions.
  - **Cons:** Increases attack surface — every URL is a potential injection point. Requires verifying multiple resources per harness rather than a single package artifact. Dependency resolution is more complex because transitive dependencies can come from disparate sources.

This granularity is intentional: the goal is to enable decentralized evolution of agent ecosystems, not just centralized package distribution.

### Repository organization for shared harnesses

To support community sharing and provide a trusted source for harness components, fullsend-ai should maintain dedicated GitHub repositories for harness files and components. Suggested structure:

- **`fullsend-ai/harnesses`** — First-class, fully supported harness definitions. These are rigorously evaluated, have test coverage, and are maintained by the fullsend team. Organizations can reference these with high confidence.

- **`fullsend-ai/community`** — Community-contributed harnesses, agents, skills, and policies. Lower evaluation bar, more experimental, and more likely to accept external contributions. Acts as a proving ground for components that may graduate to `harnesses`.

- **Tiered trust model:**
  - First-class components (from `harnesses`) could be referenced without explicit hash pinning (trust the repository).
  - Community components (from `community`) should require explicit hash pinning or signature verification.
  - External components (from arbitrary URLs) require both hash pinning and explicit allowlisting in `config.yaml`.

This repository structure makes it easier for organizations to adopt shared components while understanding the trust boundaries.

### Open questions

- **Signature verification:** Should remote resources be signed? By whom? Using what PKI?
- **Namespace governance:** Who controls `https://cdn.fullsend.ai/skills/`? How do community contributors publish?
- **Version resolution:** If a skill references `policy: v2` but doesn't specify a URL, how is that resolved?
- **Offline mode:** Should the runner support an offline mode where all resources must be pre-cached?
- **Lock file format:** What does a dependency lock file look like for harnesses?
- **Component graduation path:** How do community components graduate to first-class status? What evaluation criteria must they meet?

## Related Work

This pattern is well-established in other ecosystems:

- **GitHub Actions:** Workflows reference actions via `uses: actions/checkout@v4` (a GitHub URL shorthand). Actions are fetched at runtime. SHA pinning is recommended: `uses: actions/checkout@8e5e7e5...`.
- **Kubernetes:** Manifests reference container images by URL (`image: gcr.io/project/image:tag`). Digest pinning prevents tag mutation: `image: gcr.io/project/image@sha256:abc123...`.
- **npm/pip/cargo:** Packages reference dependencies by name+version. Lock files pin exact versions and integrity hashes.

The proposed model combines these patterns: URL-based references (like GitHub Actions) with content-addressed caching (like container images) and optional lock files (like npm).

## Implementation Plan

See `docs/plans/universal-harness-access.md` for full implementation details, security analysis, and migration path.
