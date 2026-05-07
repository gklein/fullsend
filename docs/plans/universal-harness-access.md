# Universal Harness Access

## Problem Statement

Harnesses, agents, skills, and policies are currently local-only resources resolved via relative paths within a `.fullsend` directory structure. This creates barriers to sharing, composition, and decentralized evolution of agent capabilities.

**Goal:** Make harnesses and all resources they reference universally accessible via HTTP(S) URLs, absolute paths, or relative paths, with transitive closure applying to all dependencies.

**Desired state:** An organization can run:

```bash
fullsend run https://github.com/fullsend-ai/library/harness/rust-linter.yaml
```

And the runner will:
1. Fetch the harness definition
2. Parse it to discover referenced resources (agent, skills, policies, scripts)
3. Recursively fetch any URL-referenced dependencies
4. Validate integrity and apply security policies
5. Provision the sandbox and execute the agent

All without requiring a local copy of the harness or its dependencies.

## Current State

From ADR-0024, harnesses reference resources via relative paths:

```yaml
# harness/code.yaml
agent: agents/code.md
policy: policies/code.yaml
skills:
  - skills/code-implementation
pre_script: scripts/pre-code.sh
post_script: scripts/post-code.sh
host_files:
  - src: env/gcp-vertex.env
    dest: /tmp/workspace/.env.d/gcp-vertex.env
```

Resolution logic (`internal/harness/harness.go`):
- `ResolveRelativeTo(baseDir)` converts relative paths to absolute paths
- Prevents directory traversal (e.g., `../../etc/shadow`)
- All paths must resolve within the `.fullsend` directory tree
- No network fetches; all resources must exist locally

Skills are directories with a `SKILL.md` file. Policies are OpenShell YAML files. Agent definitions are Markdown files with YAML frontmatter.

## Proposed Design

### Universal Resource Identifiers

Every path field in the harness schema accepts three forms:

1. **Relative path:** `agents/code.md` → resolved against `.fullsend` base directory
2. **Absolute path:** `/opt/fullsend/agents/code.md` → used as-is
3. **HTTPS URL:** `https://github.com/fullsend-ai/library/agents/code.md` → fetched and cached

Examples:

```yaml
# Mix local and remote resources
agent: https://github.com/fullsend-ai/library/agents/code.md
policy: policies/local-code-policy.yaml  # local override
skills:
  - https://github.com/fullsend-ai/skills/rust-conventions/SKILL.md
  - skills/org-specific-skill  # local skill
pre_script: scripts/pre-code.sh  # scripts must be local (security)
```

### Resource Types and URL Support

| Resource Type | URL Supported? | Rationale |
|---------------|----------------|-----------|
| Agent definition (`.md`) | ✅ Yes | Declarative; validated by schema |
| Policy (`.yaml`) | ✅ Yes | Declarative; validated by schema |
| Skill (`SKILL.md`) | ✅ Yes | Declarative; scanned for injection |
| Schema (`.json`) | ✅ Yes | Declarative; validated before use |
| Pre/post scripts (`.sh`) | ❌ No | Executable on host; must be local |
| Host files (certs, env) | ❌ No | Configuration; must be local |
| Container images | ✅ Yes (already) | Fetched via container registry |
| API server scripts | ❌ No | Executable; must be local |
| Validation scripts | ❌ No | Executable; must be local |

**Principle:** Declarative resources (agent definitions, skills, policies, schemas) can be remote. Executable resources (scripts, binaries) must be local to preserve auditability and prevent direct code execution from untrusted sources.

### Relative Path Resolution for URL-Referenced Resources

When a harness or resource is fetched from a URL, relative paths within that resource are resolved relative to the URL's base path, not the local `.fullsend` directory:

**Example 1: Harness fetched from URL**
```yaml
# Harness at: https://github.com/fullsend-ai/harnesses/code.yaml
agent: agents/code.md                    # → https://github.com/fullsend-ai/harnesses/agents/code.md
policy: ../policies/code-policy.yaml     # → https://github.com/fullsend-ai/policies/code-policy.yaml
skills:
  - skills/rust-linting/SKILL.md         # → https://github.com/fullsend-ai/harnesses/skills/rust-linting/SKILL.md
```

**Example 2: Skill fetched from URL**
```yaml
# Skill at: https://github.com/fullsend-ai/skills/rust-conventions/SKILL.md
---
dependencies:
  - ../common/cargo-integration/SKILL.md  # → https://github.com/fullsend-ai/skills/common/cargo-integration/SKILL.md
policy: policies/rust-sandbox.yaml        # → https://github.com/fullsend-ai/skills/rust-conventions/policies/rust-sandbox.yaml
---
```

**Resolution algorithm:**
1. If the path is absolute (`/opt/...`): use as-is (local file)
2. If the path is a URL (`https://...`): use as-is (remote resource)
3. If the path is relative (`agents/...` or `../other`):
   - If the containing resource is a URL: resolve relative to the URL's base (URL path semantics)
   - If the containing resource is local: resolve relative to `.fullsend` directory (filesystem semantics)

**Implication:** A harness author publishing a harness at `https://example.com/harnesses/code.yaml` can use relative paths to reference co-located resources, making the harness portable without hardcoding full URLs. Consumers can fetch the entire harness tree by referencing a single top-level URL.

**Security note:** URL-based relative path resolution follows RFC 3986 (URI Generic Syntax) semantics, including path traversal (`../`). The SSRF protection layer validates that resolved URLs still match allowed domain prefixes after traversal.

### Transitive Closure

A URL-referenced skill can itself reference other resources:

```yaml
# https://github.com/fullsend-ai/skills/rust-conventions/SKILL.md
---
name: rust-conventions
policy: https://github.com/fullsend-ai/policies/rust-sandbox.yaml
dependencies:
  - https://github.com/fullsend-ai/skills/cargo-integration/SKILL.md
---
# skill content
```

The runner must:
1. Parse the skill to extract its `policy` and `dependencies` references
2. Recursively fetch and validate those resources
3. Build a complete dependency graph before sandbox creation

This applies to all resource types: agents can reference skills, skills can reference policies, policies can reference schemas. The runner resolves the full transitive closure.

### Content-Addressed Caching

Fetched resources are cached locally using content addressing:

```
~/.cache/fullsend/resources/
  sha256/
    abc123.../
      metadata.json       # {url, fetch_time, content_type, headers}
      content             # the actual fetched content
```

Cache key: `SHA256(content)`
Lookup: `SHA256(URL) → cache_manifest.db → SHA256(content) → cached file`

**Why content-addressed?** If two different URLs serve identical content, they share a cache entry. This deduplicates storage and makes integrity verification uniform.

**Cache TTL:** Configurable per organization. Default: 24 hours for mutable URLs, indefinite for pinned URLs (those with `#sha256=...` fragment).

**Offline mode:** `fullsend run --offline <harness>` disables network fetches. If any required resource is not in cache, the run fails. Useful for CI environments with no internet access.

### Integrity Verification

URLs can include an integrity hash as a fragment:

```yaml
agent: https://github.com/fullsend-ai/library/agents/code.md#sha256=abc123...
```

When present, the runner:
1. Fetches the resource
2. Computes `SHA256(content)`
3. Compares to the declared hash
4. Rejects if mismatch

If no hash is provided, the resource is fetched and used (after validation), but the runner logs a warning: "Resource fetched without integrity pin."

**Recommendation:** Org-level policy should require integrity hashes for all production harnesses. Dev/experimental harnesses can omit hashes for rapid iteration.

### SSRF Protection

The URL fetch mechanism must prevent Server-Side Request Forgery attacks.

**Implemented defenses:**

1. **Protocol allowlist:** Only `https://` permitted. Reject all other protocols including insecure HTTP (`http://`) and non-HTTP protocols (`ftp://`, `file://`, `gopher://`, etc.).
2. **Domain allowlist:** Configurable in `config.yaml`:
   ```yaml
   security:
     remote_resources:
       allowed_domains:
         - github.com
         - gitlab.com
         - cdn.fullsend.ai
       # Reject all others
   ```
   **Subdomain matching:** Adding `example.com` to the allowlist also permits all subdomains (`*.example.com`). This is intentional for domains like `github.com` (where users don't control subdomains), but creates risk for domains where users can register subdomains (e.g., some cloud hosting providers). **Only allowlist domains where subdomain control is restricted.** For user-controlled hosting platforms, allowlist the specific subdomain (e.g., `myorg.cloudprovider.com`, not `cloudprovider.com`).
3. **No redirects:** HTTP 3xx responses are rejected. The URL must return 200 OK directly.
4. **Internal IP rejection:** Refuse to fetch from:
   - `127.0.0.0/8` (loopback)
   - `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (RFC 1918 private)
   - `169.254.0.0/16` (link-local)
   - `fc00::/7` (IPv6 ULA)
   - `::1` (IPv6 loopback)
5. **DNS rebinding protection:** Resolve the domain to an IP, check the IP against the blocklist, then fetch. If DNS resolves to an internal IP, reject.
6. **Timeout:** 30-second timeout on all fetches. No long-lived connections.
7. **Size limit:** Reject responses larger than 10 MB. Agents, skills, and policies should be small.

**Implementation:** New package `internal/fetch/` provides `FetchURL(url string, policy FetchPolicy) ([]byte, error)` with all defenses built in.

### Security Scanning for Remote Resources

All remote resources (agents, skills, policies) pass through the same security scanners as local resources:

- **Unicode normalization** (detect homoglyph attacks)
- **Context injection detection** (adversarial prompt patterns)
- **SSRF validation** (if the resource contains URLs, validate them)
- **Secret redaction** (reject resources containing secrets)
- **LLM Guard** (ML-based prompt injection detection)

From ADR-0024, these scanners are enabled by default with fail-closed semantics. Remote resources are scanned **before** being written to the cache, so a malicious resource is rejected at fetch time, not at use time.

**Remote resources are subject to stricter policies than local resources:**

| Check | Local Resource | Remote Resource |
|-------|----------------|-----------------|
| Schema validation | Required | Required |
| Unicode normalization | Required | Required |
| Context injection scan | Optional | **Required (no opt-out)** |
| LLM Guard threshold | 0.92 (configurable) | **0.95 (higher bar)** |
| Secret redaction | Required | Required |

This reflects the higher risk of remote resources: an attacker who controls a URL can inject content, whereas local resources are org-controlled.

### Dependency Graph and Resolution

The runner builds a directed acyclic graph (DAG) of all resources before execution:

```
harness/code.yaml
  ├─ agents/code.md (local)
  │   └─ (no dependencies)
  ├─ policies/code.yaml (local)
  │   └─ (no dependencies)
  ├─ skills/code-implementation (local)
  │   └─ (no dependencies)
  └─ https://github.com/fullsend-ai/skills/rust-conventions/SKILL.md
      ├─ https://github.com/fullsend-ai/policies/rust-sandbox.yaml
      └─ https://github.com/fullsend-ai/skills/cargo-integration/SKILL.md
          └─ (no dependencies)
```

Resolution algorithm:

1. Parse the harness YAML to extract all references
2. For each reference:
   - If local path, validate it exists
   - If URL, fetch and cache
3. Parse fetched resources to extract their references
4. Repeat step 2 for new references (depth-first traversal)
5. Detect cycles (if skill A references skill B, and skill B references skill A, reject)
6. Fail if any resource cannot be fetched or validated

**Output:** A `ResolvedHarness` struct containing absolute paths or cache paths for all resources.

**Implementation:** New package `internal/resolve/` provides `ResolveHarness(h *harness.Harness) (*ResolvedHarness, error)`.

### Runtime Dependency Loading (Future)

The current design requires all dependencies to be declared in the harness. A future enhancement would allow agents to discover and load resources at runtime:

```markdown
# Agent encounters unfamiliar code
The agent uses Bash to run: fullsend-fetch-skill rust-conventions
The runner fetches the skill if it matches allowed_remote_resources
```

This requires:

1. **Runtime fetch API:** A `fullsend-fetch-skill` binary available in the sandbox, which sends a fetch request to the runner over a Unix socket.
2. **Access policy enforcement:** The harness declares `allowed_remote_resources: ["https://github.com/fullsend-ai/skills/"]`. Runtime fetches are allowed only if the URL matches a declared prefix.
3. **Audit logging:** All runtime fetches are logged with the agent's trace ID.

**Security concern:** This expands the attack surface. An attacker who can manipulate agent input (e.g., via a crafted issue body) could trick the agent into fetching a malicious skill. Mitigations:

- Runtime fetch is **opt-in** via `allow_runtime_fetch: true` in the harness
- All fetched resources go through the same validation
- Fetch requests are rate-limited (max 10 per agent run)
- Anomalous fetch patterns trigger alerts

**Status:** Not implemented in initial design. Tracked in a future issue.

### Access Policy Model

The key challenge: **how do access policies work when agents don't know what they need until runtime?**

Proposed model (two-phase):

**Phase 1: Static declaration (implemented first)**

The harness declares all allowed remote resource prefixes:

```yaml
# harness/code.yaml
agent: agents/code.md
allowed_remote_resources:
  - https://github.com/fullsend-ai/library/
  - https://cdn.fullsend.ai/
skills:
  - https://github.com/fullsend-ai/library/skills/rust-conventions/SKILL.md
```

The runner enforces:
- All URL references in the harness must match an `allowed_remote_resources` prefix
- Transitive dependencies must also match an allowed prefix
- No runtime fetches are allowed (agent cannot fetch new resources during execution)

**Phase 2: Runtime fetch with policy (future)**

The harness declares allowed prefixes, and the agent can fetch resources at runtime if they match:

```yaml
# harness/code.yaml
agent: agents/code.md
allowed_remote_resources:
  - https://github.com/fullsend-ai/library/
allow_runtime_fetch: true
max_runtime_fetches: 10
```

During execution, the agent can fetch `https://github.com/fullsend-ai/library/skills/python-linting/SKILL.md` because it matches an allowed prefix. The runner validates and caches it.

**Audit:** All fetches (static and runtime) are logged:

```json
{
  "trace_id": "abc123",
  "fetch_time": "2026-05-07T12:34:56Z",
  "url": "https://github.com/fullsend-ai/library/skills/rust-conventions/SKILL.md",
  "sha256": "def456...",
  "fetch_type": "static",  // or "runtime"
  "allowed_by": "allowed_remote_resources[0]"
}
```

### Inheritance and Overrides

From ADR-0024, the `.fullsend` directory supports inheritance:

- Fullsend ships defaults
- Org `.fullsend` repo overlays or adds resources
- Per-repo `.fullsend/` overrides individual files

With URL support, an org can:

1. Use an upstream harness as-is:
   ```yaml
   # .fullsend/harness/rust-linter.yaml
   agent: https://github.com/fullsend-ai/library/agents/rust-linter.md
   ```

2. Override specific resources:
   ```yaml
   # .fullsend/harness/rust-linter.yaml
   agent: https://github.com/fullsend-ai/library/agents/rust-linter.md
   policy: policies/org-rust-policy.yaml  # local override
   ```

3. Per-repo override:
   ```
   my-repo/.fullsend/policies/org-rust-policy.yaml  # repo-specific policy
   ```

The resolution order remains: fullsend defaults → org `.fullsend` → per-repo `.fullsend`. URLs are resolved before inheritance—if the org harness references a URL, that URL is fetched regardless of whether fullsend's default had a local file.

## Security Implications

### Threat: Compromised URL Serves Malicious Content

**Attack:** An attacker gains control of `https://github.com/user/library/agents/code.md` and replaces it with a malicious agent definition designed to exfiltrate secrets or inject backdoors.

**Mitigations:**

1. **Integrity pinning:** Require `#sha256=...` hashes for all production harnesses. A modified resource will fail hash validation.
2. **Security scanning:** All fetched resources are scanned for injection patterns. A malicious agent definition must pass LLM Guard at a higher threshold (0.95 vs 0.92 for local).
3. **Output validation (ADR-0022):** Even if a malicious agent runs, its output is validated against a schema. Non-compliant output is rejected.
4. **Audit logging:** All fetched URLs are logged. Anomaly detection can flag unexpected URL changes.

**Residual risk:** If the attacker can produce a malicious agent that passes all scanners **and** produces schema-compliant output, it can succeed. This is the same risk as a malicious local agent—URL support does not introduce new risk here, it just extends the attack surface.

### Threat: Dependency Confusion

**Attack:** An attacker publishes a malicious skill at `https://attacker.com/skills/common-name` and tricks a harness into referencing it instead of the legitimate `https://fullsend.ai/skills/common-name`.

**Mitigations:**

1. **Explicit URLs:** Harnesses reference full URLs, not package names. There is no auto-resolution of "skill:common-name" to a URL (unlike npm, where `require('express')` resolves to the npm registry).
2. **Domain allowlist:** Org policy restricts allowed domains. `attacker.com` would be rejected unless explicitly allowed.
3. **Lock files (future):** A `harness.lock` file pins exact URLs and hashes for all transitive dependencies. Deviations trigger alerts.

### Threat: SSRF via Runner

**Attack:** An attacker crafts a harness that references `https://169.254.169.254/latest/meta-data/` (AWS metadata service) to exfiltrate cloud credentials.

**Mitigations:**

1. **Internal IP rejection:** The fetch mechanism refuses to connect to internal IPs (see SSRF Protection above).
2. **DNS rebinding protection:** Resolve domain to IP, check IP before connecting.
3. **No redirects:** A public URL cannot redirect to an internal IP.

### Threat: Prompt Injection via Malicious Skill

**Attack:** A URL-fetched skill contains adversarial instructions designed to manipulate the agent into ignoring security guardrails or exfiltrating data.

**Mitigations:**

1. **LLM Guard with higher threshold:** Remote skills are scanned at threshold 0.95 (vs 0.92 for local).
2. **Context injection detection:** Skills are scanned for known adversarial patterns.
3. **Sandbox isolation:** Skills run inside the sandbox with limited network access. They cannot directly exfiltrate data—they must produce output, which is validated.
4. **Output validation:** Even if the skill manipulates the agent, the output must conform to the declared schema.

### Threat: TOCTOU (Time-of-Check-Time-of-Use)

**Attack:** A resource is fetched and validated, but the remote server changes it between fetch and use.

**Mitigations:**

1. **Content-addressed caching:** Once fetched, the resource is cached immutably. The cache key is the content hash. The runner never re-fetches during a single run.
2. **Cache TTL:** For development, the cache can expire after 24 hours. For production, use integrity-pinned URLs (which never expire from cache).

### Threat: Malicious Script Execution

**Attack:** A harness references `pre_script: https://attacker.com/evil.sh`, which runs on the runner host with full privileges.

**Mitigations:**

1. **Scripts must be local:** Pre/post scripts, validation scripts, and API server scripts cannot be URLs. This is enforced at schema validation time.
2. **If this restriction is ever relaxed:** URL-sourced scripts must run in a restricted sandbox (separate from the agent sandbox) with no access to secrets, no network, no filesystem writes outside `/tmp`.

## Implementation Changes

### 1. Harness Schema Extension

Add `allowed_remote_resources` to the harness schema:

```yaml
# harness/code.yaml (new schema)
agent: agents/code.md
allowed_remote_resources:
  - https://github.com/fullsend-ai/library/
  - https://cdn.fullsend.ai/
skills:
  - https://github.com/fullsend-ai/library/skills/rust-conventions/SKILL.md
```

**File:** `internal/harness/harness.go`

```go
type Harness struct {
    // existing fields...
    AllowedRemoteResources []string `yaml:"allowed_remote_resources,omitempty"`
}

func (h *Harness) Validate() error {
    // existing validation...

    // Validate allowed_remote_resources entries are HTTPS URLs
    for _, prefix := range h.AllowedRemoteResources {
        u, err := url.Parse(prefix)
        if err != nil || u.Scheme != "https" {
            return fmt.Errorf("allowed_remote_resources entry %q must be an HTTPS URL", prefix)
        }
    }

    // Validate that all URL references match allowed prefixes
    for _, skill := range h.Skills {
        if isURL(skill) && !h.matchesAllowedPrefix(skill) {
            return fmt.Errorf("skill URL %q does not match allowed_remote_resources", skill)
        }
    }
    // ... repeat for agent, policy, etc.
}
```

### 2. URL Detection and Classification

**File:** `internal/harness/url.go` (new)

```go
package harness

import (
    "net/url"
    "path/filepath"
    "strings"
)

// IsURL returns true if s is an HTTP(S) URL.
// Note: This intentionally accepts both http:// and https:// for classification purposes.
// FetchURL enforces the HTTPS-only requirement at fetch time.
func IsURL(s string) bool {
    u, err := url.Parse(s)
    return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}

// isAbsPath returns true if s is an absolute file path.
func isAbsPath(s string) bool {
    return filepath.IsAbs(s)
}

// isRelPath returns true if s is a relative file path.
func isRelPath(s string) bool {
    return !IsURL(s) && !isAbsPath(s)
}

// ParseIntegrityHash extracts the SHA256 hash from a URL fragment.
// Example: https://example.com/file.md#sha256=abc123 -> "abc123"
func ParseIntegrityHash(rawURL string) (urlWithoutHash, hash string, hasHash bool) {
    u, err := url.Parse(rawURL)
    if err != nil {
        return rawURL, "", false
    }
    if u.Fragment == "" {
        return rawURL, "", false
    }
    if !strings.HasPrefix(u.Fragment, "sha256=") {
        return rawURL, "", false
    }
    hash = strings.TrimPrefix(u.Fragment, "sha256=")
    u.Fragment = ""
    return u.String(), hash, true
}
```

### 3. Resource Fetcher with SSRF Protection

**File:** `internal/fetch/fetch.go` (new)

```go
package fetch

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "net"
    "net/http"
    "net/url"
    "time"
)

type FetchPolicy struct {
    AllowedDomains []string
    MaxSizeBytes   int64
    Timeout        time.Duration
    MaxDepth       int // Maximum depth for transitive dependencies
    MaxResources   int // Maximum total resources to fetch
}

var DefaultPolicy = FetchPolicy{
    AllowedDomains: []string{"github.com", "gitlab.com", "cdn.fullsend.ai"},
    MaxSizeBytes:   10 * 1024 * 1024, // 10 MB
    Timeout:        30 * time.Second,
    MaxDepth:       10, // Maximum recursion depth for dependencies
    MaxResources:   50, // Maximum total resources fetched per harness
}

// FetchURL fetches a URL with SSRF protection and returns the content.
func FetchURL(ctx context.Context, rawURL string, policy FetchPolicy) ([]byte, error) {
    u, err := url.Parse(rawURL)
    if err != nil {
        return nil, fmt.Errorf("invalid URL: %w", err)
    }

    // 1. Only HTTPS allowed
    if u.Scheme != "https" {
        return nil, fmt.Errorf("only HTTPS URLs are allowed, got %s", u.Scheme)
    }

    // 2. Domain allowlist
    if !isAllowedDomain(u.Hostname(), policy.AllowedDomains) {
        return nil, fmt.Errorf("domain %s is not in allowed list", u.Hostname())
    }

    // 3. Resolve DNS and check for internal IPs
    ips, err := net.LookupIP(u.Hostname())
    if err != nil {
        return nil, fmt.Errorf("DNS lookup failed: %w", err)
    }
    for _, ip := range ips {
        if isInternalIP(ip) {
            return nil, fmt.Errorf("resolved to internal IP %s (SSRF protection)", ip)
        }
    }

    // 4. Fetch with timeout and size limit
    client := &http.Client{
        Timeout: policy.Timeout,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            return http.ErrUseLastResponse // No redirects
        },
    }

    resp, err := client.Get(rawURL)
    if err != nil {
        return nil, fmt.Errorf("fetch failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("fetch returned %d", resp.StatusCode)
    }

    // 5. Read body with size limit
    limited := io.LimitReader(resp.Body, policy.MaxSizeBytes+1)
    content, err := io.ReadAll(limited)
    if err != nil {
        return nil, fmt.Errorf("reading response: %w", err)
    }
    if int64(len(content)) > policy.MaxSizeBytes {
        return nil, fmt.Errorf("response exceeds maximum size of %d bytes", policy.MaxSizeBytes)
    }

    return content, nil
}

// isAllowedDomain returns true if hostname matches any allowed domain.
func isAllowedDomain(hostname string, allowed []string) bool {
    for _, domain := range allowed {
        if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
            return true
        }
    }
    return false
}

// isInternalIP returns true if ip is a loopback, private, or link-local address.
func isInternalIP(ip net.IP) bool {
    return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// ComputeSHA256 returns the hex-encoded SHA256 hash of data.
func ComputeSHA256(data []byte) string {
    hash := sha256.Sum256(data)
    return hex.EncodeToString(hash[:])
}
```

### 4. Content-Addressed Cache

**File:** `internal/fetch/cache.go` (new)

```go
package fetch

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "time"
)

type CacheEntry struct {
    URL         string    `json:"url"`
    FetchTime   time.Time `json:"fetch_time"`
    ContentType string    `json:"content_type"`
    SHA256      string    `json:"sha256"`
}

// CachePath returns ~/.cache/fullsend/resources/sha256/<hash>/
func CachePath(hash string) string {
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".cache", "fullsend", "resources", "sha256", hash)
}

// CacheGet retrieves cached content by hash. Returns nil if not cached.
func CacheGet(hash string) ([]byte, *CacheEntry, error) {
    dir := CachePath(hash)
    metaPath := filepath.Join(dir, "metadata.json")
    contentPath := filepath.Join(dir, "content")

    if _, err := os.Stat(metaPath); os.IsNotExist(err) {
        return nil, nil, nil // not cached
    }

    metaData, err := os.ReadFile(metaPath)
    if err != nil {
        return nil, nil, err
    }
    var entry CacheEntry
    if err := json.Unmarshal(metaData, &entry); err != nil {
        return nil, nil, err
    }

    content, err := os.ReadFile(contentPath)
    if err != nil {
        return nil, nil, err
    }

    return content, &entry, nil
}

// CachePut stores content in the cache.
func CachePut(url string, content []byte) error {
    hash := ComputeSHA256(content)
    dir := CachePath(hash)

    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }

    entry := CacheEntry{
        URL:       url,
        FetchTime: time.Now(),
        SHA256:    hash,
    }
    metaData, _ := json.MarshalIndent(entry, "", "  ")
    if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metaData, 0644); err != nil {
        return err
    }
    if err := os.WriteFile(filepath.Join(dir, "content"), content, 0644); err != nil {
        return err
    }

    return nil
}
```

### 5. Dependency Resolver

**File:** `internal/resolve/resolve.go` (new)

```go
package resolve

import (
    "context"
    "fmt"
    "path/filepath"
    "strings"
    "time"

    "github.com/fullsend-ai/fullsend/internal/fetch"
    "github.com/fullsend-ai/fullsend/internal/harness"
)

type ResolvedHarness struct {
    Harness      *harness.Harness
    AgentPath    string   // absolute path or cache path
    PolicyPath   string
    SkillPaths   []string
    Dependencies []Dependency
}

type Dependency struct {
    URL        string
    LocalPath  string // cache path
    SHA256     string
    FetchedAt  time.Time
}

// ResolveHarness resolves all resources (local and remote) and returns paths.
func ResolveHarness(ctx context.Context, h *harness.Harness, policy fetch.FetchPolicy) (*ResolvedHarness, error) {
    resolved := &ResolvedHarness{Harness: h}
    resourceCount := 0

    // Resolve agent
    var err error
    resolved.AgentPath, err = resolveResourceWithLimits(ctx, h.Agent, h.AllowedRemoteResources, policy, 0, &resourceCount)
    if err != nil {
        return nil, fmt.Errorf("resolving agent: %w", err)
    }

    // Resolve policy
    if h.Policy != "" {
        resolved.PolicyPath, err = resolveResourceWithLimits(ctx, h.Policy, h.AllowedRemoteResources, policy, 0, &resourceCount)
        if err != nil {
            return nil, fmt.Errorf("resolving policy: %w", err)
        }
    }

    // Resolve skills (each skill may have transitive dependencies)
    for _, skill := range h.Skills {
        skillPath, err := resolveResourceWithLimits(ctx, skill, h.AllowedRemoteResources, policy, 0, &resourceCount)
        if err != nil {
            return nil, fmt.Errorf("resolving skill %s: %w", skill, err)
        }
        resolved.SkillPaths = append(resolved.SkillPaths, skillPath)

        // Parse skill to extract transitive dependencies
        // (skill format TBD — may have a dependencies: field in frontmatter)
        // Recursively resolve those dependencies
    }

    return resolved, nil
}

// resolveResourceWithLimits resolves a single resource with depth and count limits.
func resolveResourceWithLimits(ctx context.Context, ref string, allowedPrefixes []string, policy fetch.FetchPolicy, depth int, resourceCount *int) (string, error) {
    // Check depth limit
    if depth > policy.MaxDepth {
        return "", fmt.Errorf("exceeded maximum dependency depth of %d", policy.MaxDepth)
    }

    // Check resource count limit
    if *resourceCount >= policy.MaxResources {
        return "", fmt.Errorf("exceeded maximum resource count of %d", policy.MaxResources)
    }

    if harness.IsURL(ref) {
        // Increment resource count for remote fetches
        *resourceCount++

        // Check if URL matches allowed prefixes
        if !matchesAllowedPrefix(ref, allowedPrefixes) {
            return "", fmt.Errorf("URL %s does not match allowed_remote_resources", ref)
        }

        // Parse integrity hash (if present)
        cleanURL, expectedHash, hasHash := harness.ParseIntegrityHash(ref)

        // Check cache first
        if hasHash {
            if content, _, _ := fetch.CacheGet(expectedHash); content != nil {
                return filepath.Join(fetch.CachePath(expectedHash), "content"), nil
            }
        }

        // Fetch from URL
        content, err := fetch.FetchURL(ctx, cleanURL, policy)
        if err != nil {
            return "", fmt.Errorf("fetching %s: %w", cleanURL, err)
        }

        // Verify integrity hash
        actualHash := fetch.ComputeSHA256(content)
        if hasHash && actualHash != expectedHash {
            return "", fmt.Errorf("integrity hash mismatch for %s: expected %s, got %s", cleanURL, expectedHash, actualHash)
        }

        // Store in cache
        if err := fetch.CachePut(cleanURL, content); err != nil {
            return "", fmt.Errorf("caching %s: %w", cleanURL, err)
        }

        return filepath.Join(fetch.CachePath(actualHash), "content"), nil
    }

    // Local path — return as-is (already resolved by ResolveRelativeTo)
    return ref, nil
}

func matchesAllowedPrefix(url string, allowedPrefixes []string) bool {
    for _, prefix := range allowedPrefixes {
        if strings.HasPrefix(url, prefix) {
            return true
        }
    }
    return false
}
```

### 6. CLI Integration

**File:** `internal/cli/run.go` (changes)

```go
// In runAgent():

// After loading harness and resolving paths:
h, err := harness.Load(harnessPath)
// ...
if err := h.ResolveRelativeTo(absFullsendDir); err != nil {
    return fmt.Errorf("resolving paths: %w", err)
}

// NEW: Resolve remote resources
fetchPolicy := fetch.DefaultPolicy
// TODO: Load allowed domains from config.yaml
resolved, err := resolve.ResolveHarness(ctx, h, fetchPolicy)
if err != nil {
    return fmt.Errorf("resolving remote resources: %w", err)
}

// Use resolved.AgentPath, resolved.PolicyPath, etc. instead of h.Agent, h.Policy
```

### 7. Security Scanner Integration

**File:** `internal/security/scan.go` (changes)

When a resource is fetched from a URL, it must be scanned before caching:

```go
// In fetch/fetch.go, after fetching content:

if isRemote {
    if err := security.ScanResource(content, security.RemoteResourcePolicy); err != nil {
        return nil, fmt.Errorf("security scan failed: %w", err)
    }
}
```

Remote resources use a stricter policy:

```go
// internal/security/policy.go
var RemoteResourcePolicy = ScanPolicy{
    UnicodeNormalizer: true,
    ContextInjection:  true,  // no opt-out for remote
    LLMGuard: LLMGuardConfig{
        Enabled:   true,
        Threshold: 0.95,  // higher threshold than local (0.92)
    },
}
```

### 8. Audit Logging

**File:** `internal/audit/fetch_log.go` (new)

All fetches are logged to a structured log:

```go
package audit

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "time"
)

type FetchLog struct {
    TraceID    string    `json:"trace_id"`
    FetchTime  time.Time `json:"fetch_time"`
    URL        string    `json:"url"`
    SHA256     string    `json:"sha256"`
    FetchType  string    `json:"fetch_type"`  // "static" or "runtime"
    AllowedBy  string    `json:"allowed_by"`  // which allowed_remote_resources entry matched
}

func LogFetch(log FetchLog) error {
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("getting home directory: %w", err)
    }
    logDir := filepath.Join(home, ".cache", "fullsend", "audit")
    if err := os.MkdirAll(logDir, 0755); err != nil {
        return fmt.Errorf("creating audit log directory: %w", err)
    }

    logPath := filepath.Join(logDir, "fetches.jsonl")
    f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return err
    }
    defer f.Close()

    data, _ := json.Marshal(log)
    _, err = f.Write(append(data, '\n'))
    return err
}
```

### 9. Offline Mode

Add a CLI flag to disable all network fetches:

```go
// internal/cli/run.go
cmd.Flags().Bool("offline", false, "disable network fetches (fail if harness references URLs)")

// In runAgent():
if offline && hasRemoteReferences(h) {
    return fmt.Errorf("harness references remote resources but --offline is set")
}
```

## Migration Path

### Phase 1: Read-only URL support (MVP)

- Implement URL detection, fetch, cache, SSRF protection
- Support URLs for agents, skills, policies (declarative resources only)
- Require all URL references to be declared in `allowed_remote_resources`
- No runtime fetch—all resources resolved at harness load time
- No transitive dependency resolution yet (skills cannot reference other skills)

**Deliverable:** `fullsend run` can load a harness that references `agent: https://...`

### Phase 2: Transitive dependency resolution

- Extend skill format to support `dependencies:` field in frontmatter
- Implement recursive resolution in `internal/resolve/`
- Build full dependency DAG before sandbox creation
- Detect cycles

**Deliverable:** A URL-referenced skill can itself reference other skills or policies

### Phase 3: Integrity pinning and lock files

- Support `#sha256=...` fragments in URLs
- Generate `harness.lock` file that pins all transitive dependencies
- Warn on unpinned URLs; require pinning for production harnesses

**Deliverable:** `fullsend lock harness/code.yaml` generates a lock file

### Phase 4: Runtime dependency loading

- Implement `fullsend-fetch-skill` binary for sandbox use
- Add `allow_runtime_fetch: true` flag to harness schema
- Enforce runtime fetches against `allowed_remote_resources`
- Audit log all runtime fetches

**Deliverable:** Agents can fetch skills mid-run if the harness allows it

## Testing Strategy

### Unit tests

- `internal/fetch/fetch_test.go`: Test SSRF protection (internal IPs, redirects, non-HTTPS)
- `internal/fetch/cache_test.go`: Test cache storage and retrieval
- `internal/resolve/resolve_test.go`: Test dependency resolution, cycle detection

### Integration tests

- `e2e/universal_harness_test.go`: End-to-end test of fetching a remote harness, resolving dependencies, running the agent
- Test with a mock HTTP server serving malicious resources (internal IP redirects, large responses, adversarial content)

### Security tests

- Attempt to fetch `http://` URLs (should fail)
- Attempt to fetch `https://169.254.169.254/` (should fail)
- Fetch a URL that redirects to an internal IP (should fail)
- Fetch a URL with mismatched integrity hash (should fail)
- Fetch a resource containing a known adversarial prompt (should fail LLM Guard)

## Open Questions

### 1. Signature verification

Should remote resources be cryptographically signed by their publisher?

**Options:**

- **A: No signatures (current proposal).** Rely on HTTPS and domain allowlists. Trust GitHub/GitLab to serve correct content.
- **B: GPG signatures.** Resources include a detached `.sig` file. The runner verifies against a keyring.
- **C: Sigstore/cosign.** Use Sigstore for signing (same as container images). Requires keyless signing infrastructure.

**Trade-off:** Signatures add strong provenance but require key management. For MVP, rely on HTTPS + integrity hashing. Add signatures in Phase 3.

### 2. Namespace governance

Who controls `https://cdn.fullsend.ai/skills/`? How do community contributors publish skills?

**Options:**

- **A: Centralized CDN.** Fullsend project maintains a blessed set of skills/policies at `cdn.fullsend.ai`. Contributors submit PRs to a central repo.
- **B: Decentralized publishing.** Anyone can publish skills on their own domain (e.g., `https://myorg.com/skills/`). Consumers add that domain to `allowed_remote_resources`.
- **C: Registry model (like npm).** A central registry (e.g., `registry.fullsend.ai`) where contributors can publish packages. Namespace squatting concerns apply.

**Recommendation:** Start with B (decentralized). Organizations control which domains they trust via `allowed_remote_resources`. No central gatekeeping.

### 3. Version resolution

If a skill references `policy: rust-sandbox@v2` (a name+version, not a URL), how is that resolved to a URL?

**Options:**

- **A: No name resolution.** All references must be full URLs. No "magic" resolution of names to URLs.
- **B: Registry lookup.** Names like `@fullsend/rust-sandbox@v2` are resolved via a registry API to `https://cdn.fullsend.ai/policies/rust-sandbox/v2.yaml`.
- **C: Org-level alias file.** The org defines `aliases.yaml`:
  ```yaml
  rust-sandbox@v2: https://cdn.fullsend.ai/policies/rust-sandbox/v2.yaml
  ```

**Recommendation:** Start with A (no name resolution). Use full URLs everywhere. If name resolution is needed later, introduce aliases (Option C) to avoid central registry dependency.

### 4. Cache eviction

The cache grows unbounded. When should cached resources be evicted?

**Options:**

- **A: TTL-based.** Cached resources expire after 24 hours (configurable).
- **B: LRU.** Keep the N most recently used resources.
- **C: Manual.** `fullsend cache clean` command to clear cache.

**Recommendation:** A (TTL-based) for unpinned URLs, indefinite for pinned URLs. Add `fullsend cache clean` for manual eviction.

### 5. Offline mode semantics

If `--offline` is set and a harness references a URL, should the runner:

**A:** Fail immediately (strict offline mode)
**B:** Use cached version if available, fail if not cached

**Recommendation:** B. Offline mode allows cache hits. This supports CI environments with intermittent internet.

## Related Documents

- **[ADR-0024: Harness Definitions](../ADRs/0024-harness-definitions.md)** — Current harness schema and resolution logic
- **[ADR-0022: Output Schema Enforcement](../ADRs/0022-harness-level-output-schema-enforcement.md)** — Security validation of agent output
- **[ADR-0017: Credential Isolation](../ADRs/0017-credential-isolation-for-sandboxed-agents.md)** — Sandbox security model
- **[Security Threat Model](./security-threat-model.md)** — Threat priority and attack vectors
- **[Agent Architecture](./agent-architecture.md)** — Agent composition and interaction patterns

## Conclusion

Universal harness access enables a composable, shareable ecosystem of agents, skills, and policies while introducing significant security challenges. The proposed design balances flexibility (URLs, transitive closure, runtime fetch) with security (SSRF protection, integrity hashing, stricter scanning for remote resources).

**Key principles:**

1. **Declarative resources can be remote; executable resources must be local**
2. **All fetches are logged and auditable**
3. **Remote resources are scanned more strictly than local resources**
4. **Transitive closure applies uniformly**
5. **Offline mode supports CI/CD environments**

This design should be reviewed for security implications before acceptance. See [ADR-0029](../ADRs/0029-universal-harness-access.md) for the decision record.
