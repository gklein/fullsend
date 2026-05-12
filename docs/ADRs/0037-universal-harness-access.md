---
title: "37. Universal harness access via URLs and paths"
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

# 37. Universal harness access via URLs and paths

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

**Status: Proposed** — pending security review and consensus on trust model.

This ADR is **not accepted**. The proposed approach described below is presented for discussion only. Do not implement until:
1. The implementation plan in `docs/plans/universal-harness-access.md` has been reviewed
2. Key security questions in the Open Questions section are resolved
3. Consensus is reached on the trust model for remote resources

### Proposed approach (pending security review)

**Hybrid approach: Option A for declarative resources combined with Option C's restriction on executable resources:**

- Support URLs, absolute paths, and relative paths uniformly for **declarative** harness resources (agents, skills, policies, schemas)
- **Executable resources (scripts, binaries) must be local files** (Option C restriction) to preserve auditability and prevent direct code execution from untrusted sources
- Fetch and cache remote resources content-addressed by SHA256
- Validate integrity, apply SSRF protection, and enforce per-resource policies (read-only vs executable)
- Extend transitive closure to all referenced resources
- Introduce access policies that constrain what remote resources can do (more restrictive than local resources)

**Cache location and persistence:** The cache is stored in the repository's workspace (e.g., `.fullsend-cache/` directory or similar location accessible to the workflow runner). In ephemeral CI/CD environments like GitHub Actions, where each workflow run gets a fresh runner, the cache is rebuilt on each run. To reduce fetch latency and network dependencies, the implementation should leverage the platform's native caching mechanisms (e.g., GitHub Actions cache, GitLab CI cache) to persist the content-addressed cache across workflow runs. This allows frequently-used remote resources to be restored from the platform cache rather than re-fetched from their source URLs on every run.

## Consequences

If Option A (URL support everywhere with security extensions) is accepted:

### What changes

- **Harness schema:** Every path field (`agent`, `policy`, `skills[]`, `host_files[].src`, `pre_script`, `post_script`, etc.) accepts URLs.
- **Resolution logic:** The runner resolves URLs by fetching, caching (content-addressed), and validating before use.
- **Transitive closure:** Referenced resources (skills, policies) are parsed to extract their own references, which are recursively fetched. To prevent infinite recursion and circular dependencies:
  - **Visited node tracking:** The resolver maintains a set of already-visited URLs. If a URL is encountered twice in the same dependency chain, the resolver returns an error indicating a circular dependency.
  - **Max depth limit:** Dependency resolution is bounded by a configurable maximum depth (default: 10 levels). This prevents both cycles and pathologically deep dependency trees from consuming excessive time or memory.
  - **Breadth limits:** A maximum number of dependencies per resource (default: 50) prevents dependency explosion attacks.
- **Access policies:** Runtime policies constrain what URL-referenced resources can do (e.g., URL-sourced scripts run with reduced privileges or not at all).

### Security implications (CRITICAL)

1. **TOCTOU (Time-of-Check-Time-of-Use):** A remote resource could change between fetch and use. **Mitigation:** **Mandatory hash pinning for all remote resources.** All URLs must include a SHA256 integrity hash: `https://example.com/skill.md#sha256=abc123...`. The runner verifies the fetched content matches the declared hash before use. Content-addressed caching ensures that once fetched and validated, the cached version is immutable. The cache key is `SHA256(URL + hash)`.

2. **Content injection via compromised URLs:** An attacker who controls a URL referenced by a harness can inject malicious agent instructions, skills, or policies. **Mitigations:**
   - **Mandatory hash pinning** (see above): Even if an attacker compromises the source server, they cannot change the content without breaking the hash verification. This applies equally to fullsend-ai repositories and external URLs.
   - Schema validation (ADR-0022): All fetched resources are validated against their schema before use.
   - Output validation: Agent output is validated regardless of source.
   - SSRF protection: Runner applies URL allowlists configured in `config.yaml`.
   - Signature verification (future): Remote resources could be signed by their publisher, verified by the runner.

3. **Dependency confusion:** An attacker publishes a malicious skill at `https://attacker.com/skills/common-name` and tricks a harness into referencing it instead of the legitimate `https://fullsend.ai/skills/common-name`. **Mitigations:**
   - Explicit URL references (no auto-resolution of names to URLs).
   - User-controlled URL allowlists per organization (configurable in `config.yaml`). Fetches to URLs outside the allowlist are rejected.
   - Mandatory hash pinning: The attacker cannot substitute content for an already-pinned URL.
   - Lock files (future): Pin exact URLs and hashes for all transitive dependencies.

4. **Prompt injection via skills:** A URL-fetched skill contains adversarial instructions designed to manipulate the agent. **Mitigations:**
   - All skills (local or remote) pass through the same security scanners (unicode normalization, context injection detection, LLM Guard).
   - Remote skills are subject to more restrictive policies than local skills (e.g., cannot reference executable scripts).

5. **Executable code from URLs:** Pre/post scripts fetched from URLs run on the runner host with full privileges. **Mitigation:** Apply **Option C** restriction: scripts and binaries must be local files. Only declarative resources (agents, skills, policies, schemas) can be URLs. **Alternative (future):** URL-sourced scripts could run in a restricted sandbox with no access to secrets, no network, and no filesystem writes outside `/tmp`. This requires designing an in-sandbox pre/post command execution mechanism (something like `pre_commands`/`post_commands` that run inside the sandbox before/after the agent's main execution). Today, `pre_script` and `post_script` run outside the sandbox. Any relaxation of the "scripts must be local" restriction depends on this prerequisite capability being implemented first.

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

- **Composable files, not blackbox packages:** Harnesses are not packaged as opaque bundles. Instead, they reference individual files (agent definitions, skills, policies) that can live in different locations. A harness might reference an agent from one repository, skills from another, and a policy from a third. This is more flexible and encourages fine-grained reuse — you can mix-and-match components without forking entire packages. This complements sandbox-level policy composability (#776): this ADR makes **what the agent is** composable via URLs (agent definitions, skills, policies), while #776 makes **where the agent runs** composable via provider profiles (sandbox network policies).

- **Trade-offs of granular composition:**
  - **Pros:** Encourages modular design and selective reuse. Organizations can adopt upstream agents while providing their own policies, or use community skills with organization-controlled agent definitions.
  - **Cons:** Increases attack surface — every URL is a potential injection point. Requires verifying multiple resources per harness rather than a single package artifact. Dependency resolution is more complex because transitive dependencies can come from disparate sources.

This granularity is intentional: the goal is to enable decentralized evolution of agent ecosystems, not just centralized package distribution.

### Repository organization for shared harnesses

To support community sharing and provide a trusted source for harness components, fullsend-ai should maintain a GitHub repository for harness files and components:

- **`fullsend-ai/library`** — **Composition manifests** that reference resources across repositories. These harnesses are not self-contained bundles; they reference agents, skills, and policies from various sources (this repo, security-focused skill repos like `prodsec/agent-skills`, organization-specific policy repos). This is the key value proposition of URL-based composition: harnesses can mix components from different sources without requiring a monolithic bundle. These harnesses are rigorously evaluated, have test coverage, and are maintained by the fullsend team. Organizations can reference these with high confidence.

**Note:** A separate `fullsend-ai/community` repository is not needed. With URL-based composition, anyone can share harnesses from their own repository. Getting components into a centralized "community" repo would be unnecessary overhead that contradicts the decentralization goal.

### Uniform security with user-controlled trust

**Design decision (2026-05-08):** The initial draft proposed a tiered trust model where fullsend-ai components could skip hash pinning while community and external components required increasingly strict verification. This was rejected during review because it contradicts the goal of decentralized evolution — it creates gatekeeping that discourages independent sharing and pushes everything toward centralized fullsend-ai repositories.

Instead, the model applies **uniform security to all remote resources:**

- **All remote resources require hash pinning**, regardless of source. `https://github.com/fullsend-ai/library/agents/code.md#sha256=abc123...` and `https://example.com/my-skill.md#sha256=def456...` have the same verification requirements.

- **User-controlled allowlist with sensible defaults.** Organizations configure allowed URL prefixes in `config.yaml`:
  ```yaml
  allowed_remote_resources:
    - https://github.com/fullsend-ai/library/
    # Users add their own trusted sources:
    - https://github.com/example-org/agent-library/
    - https://github.com/ralphbean/cool-skills/
  ```
  The default configuration (shipped with `.fullsend` repo creation) includes `fullsend-ai/library`, but this is user-editable and carries no special privilege beyond being in the default allowlist.

- **No special treatment for first-party resources.** A resource from `fullsend-ai/library` must be hash-pinned and pass the same integrity checks as any other URL. This prevents silent substitution attacks even if the fullsend-ai GitHub organization is compromised.

- **Trust boundary for URL-fetched harnesses:** When a harness is itself fetched from a URL, its `allowed_remote_resources` declarations cannot unilaterally expand the organization's trust boundary. The effective allowlist for URL-fetched harnesses is the **intersection** of the org-level `config.yaml` allowlist and the harness-level declarations — both must allow a domain for it to be trusted. This prevents a remote harness author from injecting access to untrusted domains.

This approach follows the GitHub Actions model: you can use actions from anywhere, but best practice is SHA-pinning everywhere. There's no tier of "blessed" actions that skip security requirements.

### Open questions

- **Signature verification (optional enhancement):** Hash pinning prevents content substitution, but doesn't prove authorship. Should remote resources optionally support cryptographic signatures? What PKI model would we use?
- **Namespace governance:** Who controls `https://cdn.fullsend.ai/skills/`? How do community contributors publish? (Note: This may not be needed — contributors can host on their own GitHub repos and users can allowlist them.)
- **Version resolution:** If a skill references `policy: v2` but doesn't specify a URL, how is that resolved?
- **Offline mode:** Should the runner support an offline mode where all resources must be pre-cached?
- **Lock file format:** What does a dependency lock file look like for harnesses?
- **git+https:// URL scheme (suggested by @deboer-tim):** Consider supporting `git+https://github.com/fullsend-ai/library.git//agents/code.md@v1.2.0#sha256=abc123...` to allow referencing by tag/commit/branch instead of bare HTTPS URLs. This gives resource owners a stable API — consumers can reference a tag or commit that won't break when internal file layout changes. This pattern is used by GitHub Actions (`uses: actions/checkout@v4`) and Terraform modules. Future extension: `oci://` refs for pulling resources from container registries.

## Related Work

This pattern is well-established in other ecosystems:

- **GitHub Actions:** Workflows reference actions via `uses: actions/checkout@v4` (a GitHub URL shorthand). Actions are fetched at runtime. SHA pinning is recommended for security: `uses: actions/checkout@8e5e7e5...`. Actions from any source (including `actions/*`) are treated equally — there's no tier of "blessed" actions that skip hash pinning.
- **Kubernetes:** Manifests reference container images by URL (`image: gcr.io/project/image:tag`). Digest pinning prevents tag mutation: `image: gcr.io/project/image@sha256:abc123...`.
- **npm/pip/cargo:** Packages reference dependencies by name+version. Lock files pin exact versions and integrity hashes.

The proposed model follows the GitHub Actions approach: URL-based references with **mandatory** SHA256 pinning (stronger than GitHub's "recommended"), content-addressed caching (like container images), and optional lock files for transitive dependencies (like npm).

## Implementation Plan

See `docs/plans/universal-harness-access.md` for full implementation details, security analysis, and migration path.
