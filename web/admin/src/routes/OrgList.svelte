<script lang="ts">
  import { onMount } from "svelte";
  import { githubUser } from "../lib/auth/session";
  import { loadToken } from "../lib/auth/tokenStore";
  import { FetchOrgsError, fetchOrgs } from "../lib/orgs/fetchOrgs";
  import { filterOrgsBySearch, type OrgRow } from "../lib/orgs/filter";

  const DISPLAY_CAP = 20;

  let orgs = $state<OrgRow[]>([]);
  let search = $state("");
  let loading = $state(false);
  let error = $state<string | null>(null);
  let emptyHint = $state<string | null>(null);

  async function loadOrgs(force: boolean) {
    const token = loadToken()?.accessToken;
    if (!token) {
      orgs = [];
      error = null;
      emptyHint = null;
      return;
    }
    loading = true;
    error = null;
    emptyHint = null;
    try {
      const r = await fetchOrgs(token, { force });
      orgs = r.orgs;
      emptyHint = r.emptyHint;
    } catch (e) {
      orgs = [];
      emptyHint = null;
      if (e instanceof FetchOrgsError) {
        error = e.message;
      } else {
        error =
          e instanceof Error ? e.message : "Failed to load organisations.";
      }
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    const unsub = githubUser.subscribe((u) => {
      if (u) void loadOrgs(false);
      else {
        orgs = [];
        error = null;
        emptyHint = null;
      }
    });
    return unsub;
  });

  const filtered = $derived(filterOrgsBySearch(orgs, search));
  const cappedRows = $derived(filtered.slice(0, DISPLAY_CAP));
  const showCapHint = $derived(filtered.length > DISPLAY_CAP);

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
    {#if loading && orgs.length === 0}
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
        <p class="cap-hint" role="status">Showing up to 20 organisations</p>
      {/if}

      {#if loading && orgs.length > 0}
        <p class="muted refresh-note" role="status">Refreshing…</p>
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
      {:else if filtered.length === 0}
        <p class="muted">
          {orgs.length === 0
            ? "No organisations found for this account."
            : "No matching organisations."}
        </p>
        {#if orgs.length === 0 && emptyHint}
          <p class="hint" role="note">{emptyHint}</p>
        {/if}
      {:else}
        <ul class="list">
          {#each cappedRows as o (o.login)}
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
  .cap-hint {
    margin: 0 0 0.75rem;
    font-size: 0.88rem;
    color: #cf222e;
    font-weight: 500;
  }
  .refresh-note {
    margin: -0.25rem 0 0.75rem;
    font-size: 0.9rem;
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
