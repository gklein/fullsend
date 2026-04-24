<script lang="ts">
  import { onMount } from "svelte";
  import { githubUser } from "../lib/auth/session";
  import { loadToken } from "../lib/auth/tokenStore";
  import { batchOrganizationsFullsendRepoExists } from "../lib/orgs/batchOrganizationsFullsendRepoGraphql";
  import { createUserOctokit } from "../lib/github/client";
  import { readTokenScopesHeaderCached } from "../lib/layers/preflight";
  import {
    FetchOrgsError,
    fetchOrgsWithProgress,
  } from "../lib/orgs/fetchOrgs";
  import { filterOrgsBySearch, type OrgRow } from "../lib/orgs/filter";
  import {
    analyzeOrgForOrgList,
    buildDeployPreflight,
    orgListRowFromAnalysis,
    type OrgListDeployRowContext,
    type OrgListRowCluster,
  } from "../lib/orgs/orgListRow";
  import {
    clearOrgListAnalysisCache,
    getOrgListAnalysisCached,
    hasOrgListAnalysisCacheEntry,
    invalidateOrgListAnalysisCacheEntry,
    setOrgListAnalysisCached,
  } from "../lib/orgs/orgListAnalysisCache";
  import type { Octokit } from "@octokit/rest";

  /** Max visible rows after filter (matches UX spec). */
  const DISPLAY_CAP = 15;
  /** After this many rows, batch UI updates until +5 more or scan completes. */
  const BATCH_FIRST = 10;
  const BATCH_INCREMENT = 5;

  let serverOrgs = $state<OrgRow[]>([]);
  let displayedOrgs = $state<OrgRow[]>([]);
  let scanComplete = $state(false);
  let search = $state("");
  let loading = $state(false);
  let error = $state<string | null>(null);
  let emptyHint = $state<string | null>(null);

  /** Batched updates while the repo scan is still running (unfiltered growth from `onProgress`). */
  function commitDisplayedRowsFromScan(capped: OrgRow[], done: boolean): void {
    if (done) {
      scanComplete = true;
      displayedOrgs = capped;
      return;
    }
    scanComplete = false;
    const c = capped.length;
    const d = displayedOrgs.length;

    if (c <= BATCH_FIRST) {
      displayedOrgs = capped;
      return;
    }
    if (c >= DISPLAY_CAP) {
      displayedOrgs = capped;
      return;
    }
    if (d < BATCH_FIRST) {
      displayedOrgs = capped.slice(0, BATCH_FIRST);
      return;
    }
    if (c >= d + BATCH_INCREMENT) {
      displayedOrgs = capped;
    }
  }

  /** Search/filter changes must not reuse scan batching (can leave rows that no longer match). */
  function applySearchFilterDisplay(): void {
    displayedOrgs = filterOrgsBySearch(serverOrgs, search).slice(0, DISPLAY_CAP);
  }

  let loadGeneration = 0;
  let loadAbort: AbortController | null = null;

  type RowUiEntry = OrgListRowCluster | "pending";

  let rowUi = $state<Record<string, RowUiEntry>>({});
  let rowEvalGen = 0;

  async function readDeployPreflightOrSkipped(octokit: Octokit, accessToken: string) {
    try {
      return buildDeployPreflight(
        await readTokenScopesHeaderCached(octokit, accessToken),
      );
    } catch {
      return buildDeployPreflight(null);
    }
  }

  function orgRowContext(login: string): OrgListDeployRowContext {
    const row =
      serverOrgs.find((x) => x.login === login) ??
      displayedOrgs.find((x) => x.login === login);
    return {
      hasWritePathInOrg: row?.hasWritePathInOrg ?? false,
      membershipCanCreateRepository: row?.membershipCanCreateRepository ?? null,
    };
  }

  async function refreshOrgRow(login: string): Promise<void> {
    const token = loadToken()?.accessToken;
    if (!token) return;
    invalidateOrgListAnalysisCacheEntry(login);
    rowUi = { ...rowUi, [login]: "pending" };
    try {
      const octokit = createUserOctokit(token);
      const deployPreflight = await readDeployPreflightOrSkipped(octokit, token);
      const hints = await batchOrganizationsFullsendRepoExists(octokit, [login]);
      const hint = hints.get(login.trim().toLowerCase()) ?? null;
      const res = await analyzeOrgForOrgList(login, octokit, {
        fullsendRepoExistsHint: hint,
      });
      if (res.kind === "ok") {
        setOrgListAnalysisCached(login, res);
      }
      rowUi = {
        ...rowUi,
        [login]: orgListRowFromAnalysis(res, deployPreflight, orgRowContext(login)),
      };
    } catch (e) {
      rowUi = {
        ...rowUi,
        [login]: {
          kind: "error",
          message:
            e instanceof Error ? e.message : "Failed to evaluate organisation.",
        },
      };
    }
  }

  /** Stable `id` / `popovertarget` token for each org row (GitHub logins are `[A-Za-z0-9-]`). */
  function cannotDeployPopoverId(login: string): string {
    return `cd-${login.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
  }

  /** Debounced: search-as-you-type must not re-hit GitHub for every keystroke. */
  const ROW_ANALYSIS_DEBOUNCE_MS = 280;
  /** Pause between sequential org analyses (display order) to avoid REST bursts. */
  const ORG_ROW_ANALYSIS_THROTTLE_MS = 400;

  $effect(() => {
    const orgs = displayedOrgs;
    const token = loadToken()?.accessToken;
    const user = $githubUser;
    if (!token || !user) {
      rowUi = {};
      return;
    }

    let alive = true;
    const gen = (rowEvalGen += 1);
    const octokit = createUserOctokit(token);

    const t = setTimeout(() => {
      if (!alive) return;
      const pending: Record<string, RowUiEntry> = {};
      for (const o of orgs) {
        pending[o.login] = "pending";
      }
      rowUi = pending;

      void (async () => {
        const deployPreflight = await readDeployPreflightOrSkipped(octokit, token);
        if (!alive || gen !== rowEvalGen) return;

        const pending: Record<string, RowUiEntry> = {};
        for (const o of orgs) {
          const cached = getOrgListAnalysisCached(o.login);
          pending[o.login] = cached
            ? orgListRowFromAnalysis(cached, deployPreflight, orgRowContext(o.login))
            : "pending";
        }
        rowUi = pending;

        const needNetwork = orgs.filter((o) => !hasOrgListAnalysisCacheEntry(o.login));
        let hints = new Map<string, boolean | null>();
        if (needNetwork.length > 0) {
          hints = await batchOrganizationsFullsendRepoExists(
            octokit,
            needNetwork.map((o) => o.login),
          );
        }
        if (!alive || gen !== rowEvalGen) return;

        for (let idx = 0; idx < needNetwork.length; idx++) {
          const o = needNetwork[idx]!;
          if (!alive || gen !== rowEvalGen) return;
          const hintKey = o.login.trim().toLowerCase();
          const hint = hints.get(hintKey) ?? null;
          const res = await analyzeOrgForOrgList(o.login, octokit, {
            fullsendRepoExistsHint: hint,
          });
          if (!alive || gen !== rowEvalGen) return;
          if (res.kind === "ok") {
            setOrgListAnalysisCached(o.login, res);
          }
          rowUi = {
            ...rowUi,
            [o.login]: orgListRowFromAnalysis(res, deployPreflight, orgRowContext(o.login)),
          };
          if (idx < needNetwork.length - 1) {
            await new Promise((r) => setTimeout(r, ORG_ROW_ANALYSIS_THROTTLE_MS));
          }
        }
      })();
    }, ROW_ANALYSIS_DEBOUNCE_MS);

    return () => {
      alive = false;
      clearTimeout(t);
    };
  });

  async function loadOrgs(force: boolean) {
    const token = loadToken()?.accessToken;
    if (!token) {
      loadAbort?.abort();
      loadAbort = null;
      loadGeneration += 1;
      serverOrgs = [];
      displayedOrgs = [];
      scanComplete = false;
      error = null;
      emptyHint = null;
      loading = false;
      return;
    }
    if (force) {
      clearOrgListAnalysisCache();
    }
    loadAbort?.abort();
    loadAbort = new AbortController();
    const signal = loadAbort.signal;
    const gen = (loadGeneration += 1);

    loading = true;
    error = null;
    emptyHint = null;
    serverOrgs = [];
    displayedOrgs = [];
    scanComplete = false;
    try {
      const r = await fetchOrgsWithProgress(token, {
        force,
        signal,
        onProgress: (orgs, meta) => {
          if (gen !== loadGeneration) return;
          serverOrgs = orgs;
          const capped = filterOrgsBySearch(orgs, search).slice(0, DISPLAY_CAP);
          commitDisplayedRowsFromScan(capped, meta.done);
        },
      });
      if (gen !== loadGeneration) return;
      serverOrgs = r.orgs;
      emptyHint = r.emptyHint;
      const capped = filterOrgsBySearch(r.orgs, search).slice(0, DISPLAY_CAP);
      commitDisplayedRowsFromScan(capped, true);
    } catch (e) {
      if (gen !== loadGeneration) return;
      if (e instanceof DOMException && e.name === "AbortError") {
        return;
      }
      serverOrgs = [];
      displayedOrgs = [];
      scanComplete = false;
      emptyHint = null;
      if (e instanceof FetchOrgsError) {
        error = e.message;
      } else {
        error =
          e instanceof Error ? e.message : "Failed to load organisations.";
      }
    } finally {
      if (gen === loadGeneration) {
        loading = false;
      }
    }
  }

  onMount(() => {
    const unsub = githubUser.subscribe((u) => {
      if (u) void loadOrgs(false);
      else {
        loadAbort?.abort();
        loadAbort = null;
        loadGeneration += 1;
        serverOrgs = [];
        displayedOrgs = [];
        scanComplete = false;
        error = null;
        emptyHint = null;
        loading = false;
      }
    });
    return unsub;
  });

  const filteredAll = $derived(filterOrgsBySearch(serverOrgs, search));
  const showCapHint = $derived(filteredAll.length > DISPLAY_CAP);

  function orgAvatarUrl(login: string): string {
    return `https://github.com/${encodeURIComponent(login)}.png?size=64`;
  }
</script>

<section class="orgs" aria-labelledby="orgs-h">
  <h1 id="orgs-h">
    Select an organisation to deploy or configure Fullsend
  </h1>

  {#if !$githubUser}
    <p class="muted">Sign in to load this list.</p>
  {:else}
    {#if loading && serverOrgs.length === 0}
      <div class="org-loading" role="status" aria-live="polite" aria-busy="true">
        <div class="org-loading-spinner" aria-hidden="true"></div>
        <p class="org-loading-label">Loading organisations…</p>
      </div>
    {:else}
      <div class="toolbar">
        <label class="search-label">
          <span class="sr-only">Filter organisations</span>
          <input
            type="search"
            class="search"
            placeholder="Type to filter"
            bind:value={search}
            oninput={() => applySearchFilterDisplay()}
            autocomplete="off"
            spellcheck="false"
          />
        </label>
        <button
          type="button"
          class="btn"
          disabled={loading}
          onclick={() => void loadOrgs(true)}
        >
          Refresh
        </button>
      </div>

      {#if showCapHint}
        <p class="cap-hint" role="status">Showing up to 15 organisations</p>
      {/if}

      {#if error}
        <div class="banner banner--err" role="alert">
          <span class="banner-msg">{error}</span>
          <button
            type="button"
            class="btn banner-retry"
            onclick={() => void loadOrgs(true)}
          >
            Retry
          </button>
        </div>
      {:else if filteredAll.length === 0}
        <p class="muted">
          {serverOrgs.length === 0
            ? "No organisations found for this account."
            : "No matching organisations."}
        </p>
        {#if serverOrgs.length === 0 && emptyHint}
          <p class="hint" role="note">{emptyHint}</p>
        {/if}
      {:else}
        <ul class="list">
          {#each displayedOrgs as o (o.login)}
            {@const ui = rowUi[o.login]}
            <li class="row">
              <div class="row-main">
                <img
                  class="org-avatar"
                  src={orgAvatarUrl(o.login)}
                  alt=""
                  width="36"
                  height="36"
                  loading="lazy"
                />
                <span class="org-name">{o.login}</span>
              </div>
              <div class="row-actions">
                {#if ui === undefined || ui === "pending"}
                  <div
                    class="row-spinner"
                    role="status"
                    aria-live="polite"
                    aria-busy="true"
                    aria-label="Checking deployment state"
                  >
                    <span class="row-spinner-disc" aria-hidden="true"></span>
                  </div>
                {:else if ui.kind === "configure"}
                  <a
                    class="btn btn-muted"
                    href="#/org/{encodeURIComponent(o.login)}"
                  >
                    Configure
                  </a>
                {:else if ui.kind === "deploy"}
                  <a
                    class="btn btn-primary"
                    href="#/install/{encodeURIComponent(o.login)}"
                  >
                    Deploy Fullsend
                  </a>
                {:else if ui.kind === "cannot_deploy"}
                  {@const cdId = cannotDeployPopoverId(o.login)}
                  <div class="cannot-deploy">
                    <span class="warn-icon" aria-hidden="true">⚠</span>
                    <span class="cannot-deploy-label">Cannot deploy</span>
                    <button
                      type="button"
                      class="info-btn"
                      popovertarget={cdId}
                      aria-haspopup="dialog"
                      aria-label={`Details for why ${o.login} cannot deploy`}
                    >
                      i
                    </button>
                    <div id={cdId} class="cannot-deploy-popover" popover>
                      <p class="cannot-deploy-popover-lead">{ui.reason}</p>
                      {#if ui.githubApiMessage}
                        <p class="cannot-deploy-popover-api">{ui.githubApiMessage}</p>
                      {/if}
                      {#if ui.missingPermissionLines?.length}
                        <p class="cannot-deploy-popover-sub">Details from GitHub:</p>
                        <ul class="cannot-deploy-popover-list">
                          {#each ui.missingPermissionLines as line}
                            <li>{line}</li>
                          {/each}
                        </ul>
                      {/if}
                      {#if ui.helpBullets?.length}
                        <p class="cannot-deploy-popover-sub">What you can try:</p>
                        <ul class="cannot-deploy-popover-list">
                          {#each ui.helpBullets as line}
                            <li>{line}</li>
                          {/each}
                        </ul>
                      {/if}
                    </div>
                  </div>
                {:else if ui.kind === "error"}
                  <div class="row-err">
                    <span class="err-icon" aria-hidden="true">▲</span>
                    <span class="row-err-label">Error</span>
                    <span class="row-err-msg" title={ui.message}>
                      {ui.message}
                    </span>
                    <button
                      type="button"
                      class="btn row-err-retry"
                      onclick={() => void refreshOrgRow(o.login)}
                    >
                      Retry
                    </button>
                  </div>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
        {#if loading && displayedOrgs.length > 0}
          <div
            class="org-more-loading"
            role="status"
            aria-live="polite"
            aria-busy="true"
          >
            <div class="org-more-spinner" aria-hidden="true"></div>
            <span class="sr-only">Loading more organisations</span>
          </div>
        {/if}
      {/if}
    {/if}
  {/if}
</section>

<style>
  .orgs {
    max-width: 42rem;
  }
  .orgs h1 {
    margin: 0 0 1rem;
    font-size: 1.15rem;
    font-weight: 600;
    line-height: 1.35;
  }
  .org-loading {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1rem;
    padding: 2.5rem 1rem;
    min-height: 8rem;
  }
  .org-loading-spinner {
    width: 2.25rem;
    height: 2.25rem;
    border: 3px solid #d0d7de;
    border-top-color: #24292f;
    border-radius: 50%;
    animation: org-spin 0.75s linear infinite;
  }
  .org-loading-label {
    margin: 0;
    font-size: 0.95rem;
    color: #444;
  }
  @keyframes org-spin {
    to {
      transform: rotate(360deg);
    }
  }
  .org-more-loading {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: calc(5 * 2.75rem);
    margin-top: 0.25rem;
    border: 1px dashed #d0d7de;
    border-radius: 8px;
    background: #fafafa;
  }
  .org-more-spinner {
    width: 2rem;
    height: 2rem;
    border: 3px solid #d0d7de;
    border-top-color: #24292f;
    border-radius: 50%;
    animation: org-spin 0.75s linear infinite;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: center;
    margin-bottom: 0.5rem;
  }
  .cap-hint {
    margin: 0 0 0.75rem;
    font-size: 0.88rem;
    color: #cf222e;
    font-weight: 500;
  }
  .search-label {
    flex: 1;
    min-width: 12rem;
  }
  .search {
    width: 100%;
    box-sizing: border-box;
    padding: 0.4rem 0.6rem;
    font: inherit;
    border: 1px solid #ccc;
    border-radius: 6px;
  }
  .btn {
    cursor: pointer;
    padding: 0.4rem 0.75rem;
    border: 1px solid #888;
    border-radius: 6px;
    background: #f4f4f4;
    font: inherit;
  }
  .btn:focus-visible {
    outline: 2px solid #0969da;
    outline-offset: 2px;
  }
  .btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  .row-actions a.btn {
    text-decoration: none;
    display: inline-flex;
    align-items: center;
    box-sizing: border-box;
  }
  .btn-muted {
    background: #eaeaea;
    border-color: #bbb;
    color: #333;
  }
  .btn-primary {
    background: #0969da;
    border-color: #0969da;
    color: #fff;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
  .list {
    list-style: none;
    margin: 0.75rem 0 0;
    padding: 0;
    border: 1px solid #ddd;
    border-radius: 8px;
    overflow: hidden;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.65rem;
    padding: 0.55rem 0.75rem;
    border-bottom: 1px solid #eee;
  }
  .row:last-child {
    border-bottom: none;
  }
  .row-main {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    min-width: 0;
  }
  .org-avatar {
    border-radius: 6px;
    flex-shrink: 0;
  }
  .org-name {
    font-size: 0.95rem;
    font-weight: 500;
    word-break: break-word;
  }
  .row-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    align-items: center;
    min-height: 2.25rem;
  }
  .row-spinner {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
  }
  .row-spinner-disc {
    display: block;
    width: 1.25rem;
    height: 1.25rem;
    border: 2px solid #d0d7de;
    border-top-color: #24292f;
    border-radius: 50%;
    animation: org-spin 0.75s linear infinite;
  }
  .cannot-deploy {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.88rem;
    color: #9a6700;
  }
  .warn-icon {
    font-size: 1rem;
    line-height: 1;
  }
  .cannot-deploy-label {
    font-weight: 600;
  }
  .info-btn {
    box-sizing: border-box;
    min-width: 1.35rem;
    height: 1.35rem;
    padding: 0;
    border-radius: 999px;
    border: 1px solid #bf8700;
    background: #fff8c5;
    color: #7d4e00;
    font-size: 0.72rem;
    font-weight: 700;
    font-style: italic;
    cursor: help;
    line-height: 1;
  }
  .info-btn:focus-visible {
    outline: 2px solid #0969da;
    outline-offset: 2px;
  }
  .cannot-deploy-popover {
    max-width: min(22rem, calc(100vw - 2rem));
    padding: 0.75rem 0.85rem;
    border: 1px solid #d4a72c;
    border-radius: 8px;
    background: #fffef5;
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.12);
    color: #24292f;
    font-size: 0.85rem;
    line-height: 1.45;
  }
  .cannot-deploy-popover-lead {
    margin: 0 0 0.5rem;
    font-weight: 500;
  }
  .cannot-deploy-popover-api {
    margin: 0 0 0.5rem;
    font-size: 0.8rem;
    color: #57606a;
    word-break: break-word;
  }
  .cannot-deploy-popover-sub {
    margin: 0.5rem 0 0.35rem;
    font-weight: 600;
    font-size: 0.82rem;
  }
  .cannot-deploy-popover-list {
    margin: 0;
    padding-left: 1.15rem;
  }
  .cannot-deploy-popover-list li {
    margin: 0.2rem 0;
  }
  .row-err {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem;
    max-width: 18rem;
    font-size: 0.85rem;
    color: #a40e26;
  }
  .err-icon {
    font-size: 0.75rem;
    line-height: 1;
  }
  .row-err-label {
    font-weight: 700;
  }
  .row-err-msg {
    flex: 1 1 8rem;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .row-err-retry {
    flex-shrink: 0;
    padding: 0.25rem 0.5rem;
    font-size: 0.82rem;
  }
  .muted {
    color: #555;
    margin: 0 0 0.75rem;
  }
  .hint {
    margin: 0 0 0.75rem;
    padding: 0.65rem 0.75rem;
    font-size: 0.9rem;
    line-height: 1.45;
    color: #333;
    background: #f6f8fa;
    border: 1px solid #d8dee4;
    border-radius: 6px;
    max-width: 40rem;
  }
  .banner {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.65rem 1rem;
    padding: 0.65rem 0.75rem;
    margin: 0.5rem 0 0;
    border: 1px solid #ffc1c1;
    border-radius: 8px;
    background: #ffeef0;
    font-size: 0.92rem;
  }
  .banner-msg {
    flex: 1;
    min-width: 10rem;
    color: #24292f;
  }
  .banner-retry {
    flex-shrink: 0;
  }
</style>
