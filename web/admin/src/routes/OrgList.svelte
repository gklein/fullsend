<script lang="ts">
  import { onMount } from "svelte";
  import { githubUser } from "../lib/auth/session";
  import { loadGithubAppSlug, loadToken } from "../lib/auth/tokenStore";
  import { githubAppInstallationsNewUrl } from "../lib/github/githubAppInstallLink";
  import {
    FetchOrgsError,
    fetchOrgsWithProgress,
    INSTALLATIONS_PER_PAGE,
    MAX_INSTALLATION_LIST_PAGES,
  } from "../lib/orgs/fetchOrgs";
  import { filterOrgsBySearch, type OrgRow } from "../lib/orgs/filter";

  /** Delays after an empty install list response for silent GitHub API rechecks (ms). */
  const EMPTY_LIST_RECHECK_DELAYS_MS = [14_000, 32_000, 55_000] as const;

  /** Abort stuck installation-list fetches so Refresh never stays disabled indefinitely. */
  const ORG_LIST_FETCH_TIMEOUT_MS = 60_000;

  type LoadOrgsOpts = {
    /** When true and the list is still empty after success, schedule delayed rechecks. */
    allowEmptyFollowUpPoll?: boolean;
    /** True for scheduled rechecks: do not cancel sibling timers or bump the poll session. */
    internalPoll?: boolean;
  };

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
  /** Slug for GitHub “install app” link: API result merged with OAuth-persisted slug. */
  let resolvedAppSlug = $state<string | null>(null);
  /** True when `GET /user/installations` pagination stopped at the safety page cap. */
  let installationListTruncated = $state(false);

  /** After the first completed fetch, manual refresh keeps the list on screen (no full-page blink). */
  let hasCompletedOrgFetchOnce = $state(false);
  /** True while a non-initial refresh is in flight (list not cleared). */
  let inlineListRefresh = $state(false);
  /** Epoch ms of last successful installations fetch (for user feedback). */
  let listCheckAt = $state<number | null>(null);
  /** Bumped on user-facing loads so delayed recheck callbacks from an older session no-op. */
  let pollSession = 0;
  let pollTimeouts: number[] = [];

  function clearPollTimeouts(): void {
    for (const id of pollTimeouts) clearTimeout(id);
    pollTimeouts = [];
  }

  function scheduleEmptyListRechecks(): void {
    const sid = pollSession;
    for (const delayMs of EMPTY_LIST_RECHECK_DELAYS_MS) {
      const id = window.setTimeout(() => {
        if (pollSession !== sid) return;
        void loadOrgs(true, { internalPoll: true });
      }, delayMs);
      pollTimeouts.push(id);
    }
  }

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

  async function loadOrgs(force: boolean, opts?: LoadOrgsOpts) {
    const token = loadToken()?.accessToken;
    if (!token) {
      loadAbort?.abort();
      loadAbort = null;
      loadGeneration += 1;
      clearPollTimeouts();
      pollSession += 1;
      serverOrgs = [];
      displayedOrgs = [];
      scanComplete = false;
      error = null;
      emptyHint = null;
      resolvedAppSlug = null;
      installationListTruncated = false;
      hasCompletedOrgFetchOnce = false;
      inlineListRefresh = false;
      listCheckAt = null;
      loading = false;
      return;
    }

    const internalPoll = opts?.internalPoll === true;
    if (!internalPoll) {
      clearPollTimeouts();
      pollSession += 1;
    }

    loadAbort?.abort();
    loadAbort = new AbortController();
    const signal = loadAbort.signal;
    const gen = (loadGeneration += 1);

    const skipClearLists = force && hasCompletedOrgFetchOnce;
    inlineListRefresh = skipClearLists;

    loading = true;
    error = null;
    if (!skipClearLists) {
      emptyHint = null;
      installationListTruncated = false;
      serverOrgs = [];
      displayedOrgs = [];
    }
    scanComplete = false;

    let fetchTimedOut = false;
    let fetchTimeoutId: ReturnType<typeof setTimeout> | undefined;

    try {
      fetchTimeoutId = window.setTimeout(() => {
        fetchTimedOut = true;
        loadAbort?.abort();
      }, ORG_LIST_FETCH_TIMEOUT_MS);

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
      installationListTruncated = r.installationListTruncated;
      resolvedAppSlug = r.appSlugFromApi ?? loadGithubAppSlug();
      const capped = filterOrgsBySearch(r.orgs, search).slice(0, DISPLAY_CAP);
      commitDisplayedRowsFromScan(capped, true);
      listCheckAt = Date.now();
      if (
        opts?.allowEmptyFollowUpPoll &&
        r.orgs.length === 0 &&
        !signal.aborted
      ) {
        scheduleEmptyListRechecks();
      }
      hasCompletedOrgFetchOnce = true;
    } catch (e) {
      if (gen !== loadGeneration) return;
      if (e instanceof DOMException && e.name === "AbortError") {
        if (fetchTimedOut) {
          error =
            "Refreshing organisations timed out. Check your connection and try again.";
          hasCompletedOrgFetchOnce = true;
        }
        return;
      }
      if (!skipClearLists) {
        serverOrgs = [];
        displayedOrgs = [];
      }
      scanComplete = false;
      emptyHint = null;
      installationListTruncated = false;
      resolvedAppSlug = null;
      if (e instanceof FetchOrgsError) {
        error = e.message;
      } else {
        error =
          e instanceof Error ? e.message : "Failed to load organisations.";
      }
      hasCompletedOrgFetchOnce = true;
    } finally {
      if (fetchTimeoutId !== undefined) {
        clearTimeout(fetchTimeoutId);
      }
      if (gen === loadGeneration) {
        loading = false;
        inlineListRefresh = false;
      }
    }
  }

  onMount(() => {
    const unsub = githubUser.subscribe((u) => {
      if (u) void loadOrgs(false, { allowEmptyFollowUpPoll: true });
      else {
        loadAbort?.abort();
        loadAbort = null;
        loadGeneration += 1;
        clearPollTimeouts();
        pollSession += 1;
        serverOrgs = [];
        displayedOrgs = [];
        scanComplete = false;
        error = null;
        emptyHint = null;
        resolvedAppSlug = null;
        installationListTruncated = false;
        hasCompletedOrgFetchOnce = false;
        inlineListRefresh = false;
        listCheckAt = null;
        loading = false;
      }
    });
    return unsub;
  });

  const filteredAll = $derived(filterOrgsBySearch(serverOrgs, search));
  const showCapHint = $derived(filteredAll.length > DISPLAY_CAP);

  const installAppHref = $derived(
    githubAppInstallationsNewUrl((resolvedAppSlug ?? "").trim()),
  );

  function orgAvatarUrl(login: string): string {
    return `https://github.com/${encodeURIComponent(login)}.png?size=64`;
  }

  function formatListCheckTime(ts: number): string {
    return new Date(ts).toLocaleTimeString(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  }
</script>

<section class="orgs" aria-labelledby="orgs-h">
  <h1 id="orgs-h">
    Select an organisation to deploy or configure Fullsend
  </h1>

  {#if !$githubUser}
    <p class="muted">Sign in to load this list.</p>
  {:else}
    {#if loading && serverOrgs.length === 0 && !inlineListRefresh}
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
          class="btn btn-refresh"
          disabled={loading}
          aria-busy={loading}
          onclick={() => void loadOrgs(true, { allowEmptyFollowUpPoll: true })}
        >
          {#if loading}
            <span class="btn-refresh-spinner" aria-hidden="true"></span>
          {/if}
          <span>Refresh</span>
        </button>
      </div>

      {#if listCheckAt != null && !error && !loading}
        <p class="list-check-at" role="status">
          {#if serverOrgs.length === 0}
            Last checked at {formatListCheckTime(listCheckAt)}. If you just installed the app,
            GitHub can take a minute or longer before it shows up here. After you use Refresh, this
            page also rechecks a few times in the background while you stay on it.
          {:else}
            Organisations last refreshed at {formatListCheckTime(listCheckAt)}. Use Refresh after
            you add or remove installs on GitHub; changes can take a short while to appear.
          {/if}
        </p>
      {/if}

      {#if showCapHint}
        <p class="cap-hint" role="status">Showing up to 15 organisations</p>
      {/if}

      {#if installationListTruncated && !error}
        <p class="cap-hint" role="status">
          Organisation list may be incomplete — loading stopped after
          {MAX_INSTALLATION_LIST_PAGES} pages of GitHub app installations ({INSTALLATIONS_PER_PAGE} per
          page). Use <strong>Refresh</strong> after you change installs; if you need more rows, ask your
          operator to raise the page cap in code.
        </p>
      {/if}

      {#if error}
        <div class="banner banner--err" role="alert">
          <span class="banner-msg">{error}</span>
          <button
            type="button"
            class="btn banner-retry"
            onclick={() => void loadOrgs(true, { allowEmptyFollowUpPoll: true })}
          >
            Retry
          </button>
        </div>
      {:else if filteredAll.length === 0}
        {#if serverOrgs.length === 0}
          {#if emptyHint}
            <p class="hint hint--empty" role="note">{emptyHint}</p>
          {:else}
            <p class="muted">No organisations found for this account.</p>
          {/if}
        {:else}
          <p class="muted">No matching organisations.</p>
        {/if}
      {:else}
        <ul class="list">
          {#each displayedOrgs as o (o.login)}
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
                <button type="button" class="btn btn-muted" disabled title="Coming soon">
                  Configure
                </button>
                <button type="button" class="btn btn-primary" disabled title="Coming soon">
                  Deploy Fullsend
                </button>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}

    <div class="install-app-block" aria-labelledby="install-app-h">
      <h2 id="install-app-h" class="install-app-heading">Fullsend Admin app</h2>
      {#if serverOrgs.length === 0}
        <p class="install-app-copy">
          After you install or change access on GitHub, click <strong>Refresh</strong> at the top of
          this page. GitHub does not return you here automatically. It can take a minute or longer
          before a new install appears in GitHub’s data — after you refresh, we also recheck a few
          times in the background when the list is still empty.
        </p>
        <p class="install-app-line">
          {#if installAppHref}
            <a
              class="orgs-plain-link"
              href={installAppHref}
              target="_blank"
              rel="noopener noreferrer"
            >
              Install the Fullsend Admin app on GitHub
            </a>
            <span class="install-app-after-link"> (opens in a new tab)</span>
          {:else}
            <span class="install-app-unavailable-inline">
              Install link is unavailable (app slug not known). Your operator can set
              <code>GITHUB_APP_SLUG</code> on the site Worker or ensure installations return a slug.
            </span>
          {/if}
        </p>
      {:else}
        <p class="install-app-copy">
          To deploy or configure Fullsend for another organisation, install the Fullsend Admin app
          there. When you return from GitHub, click <strong>Refresh</strong> at the top of this page.
        </p>
        {#if installAppHref}
          <p class="install-app-line">
            <a
              class="orgs-plain-link"
              href={installAppHref}
              target="_blank"
              rel="noopener noreferrer"
            >
              Install the Fullsend Admin app on GitHub
            </a>
            <span class="install-app-after-link"> (opens in a new tab)</span>
          </p>
        {:else}
          <p class="muted install-app-unavailable">
            Install link is unavailable (app slug not known). Your operator can set
            <code>GITHUB_APP_SLUG</code> on the site Worker or ensure installations return a slug.
          </p>
        {/if}
      {/if}
    </div>
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
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: center;
    margin-bottom: 0.5rem;
  }
  .list-check-at {
    margin: 0 0 0.75rem;
    font-size: 0.88rem;
    line-height: 1.45;
    color: #444;
    max-width: 40rem;
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
  .btn-refresh {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
  }
  .btn-refresh-spinner {
    width: 0.95rem;
    height: 0.95rem;
    border: 2px solid #b0b8c1;
    border-top-color: #24292f;
    border-radius: 50%;
    animation: org-spin 0.75s linear infinite;
    flex-shrink: 0;
  }
  .btn-refresh:disabled {
    opacity: 0.88;
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
  .hint--empty {
    margin-bottom: 1rem;
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
  .install-app-block {
    margin-top: 1.25rem;
    padding-top: 1rem;
    border-top: 1px solid #d8dee4;
  }
  .install-app-heading {
    margin: 0 0 0.5rem;
    font-size: 1rem;
    font-weight: 600;
  }
  .install-app-copy {
    margin: 0 0 0.65rem;
    font-size: 0.9rem;
    line-height: 1.45;
    color: #444;
    max-width: 40rem;
  }
  .install-app-line {
    margin: 0 0 0.65rem;
    font-size: 0.9rem;
    line-height: 1.45;
    max-width: 40rem;
  }
  /**
   * Plain inline link — explicit reset so host or UA styles cannot turn this into a “button” link.
   */
  .orgs-plain-link,
  .orgs-plain-link:visited {
    appearance: none;
    display: inline;
    margin: 0;
    padding: 0;
    border: 0;
    border-radius: 0;
    background: transparent;
    box-shadow: none;
    font: inherit;
    font-weight: 400;
    line-height: inherit;
    color: #0969da;
    text-decoration: underline;
    text-underline-offset: 0.15em;
    cursor: pointer;
  }
  .orgs-plain-link:hover {
    color: #0550ae;
  }
  .orgs-plain-link:focus-visible {
    outline: 2px solid #0969da;
    outline-offset: 2px;
    border-radius: 2px;
  }
  .install-app-after-link {
    font-size: 0.9rem;
    color: #57606a;
    font-weight: 400;
  }
  .install-app-unavailable-inline {
    color: #57606a;
    font-weight: 400;
  }
  .install-app-unavailable {
    margin: 0;
    font-size: 0.88rem;
    max-width: 40rem;
  }
  .install-app-unavailable code {
    font-size: 0.85em;
  }
</style>
