<script lang="ts">
  import { onMount } from "svelte";
  import { githubUser } from "../lib/auth/session";
  import { loadToken } from "../lib/auth/tokenStore";
  import { FetchOrgsError, fetchOrgs } from "../lib/orgs/fetchOrgs";
  import { filterOrgsByPrefix, type OrgRow } from "../lib/orgs/filter";

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
          e instanceof Error ? e.message : "Failed to load organizations.";
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

  const filtered = $derived(filterOrgsByPrefix(orgs, search));
</script>

<section class="orgs" aria-labelledby="orgs-h">
  <h1 id="orgs-h">Organizations</h1>

  {#if !$githubUser}
    <p class="muted">Sign in to list organizations you belong to.</p>
  {:else}
    <div class="toolbar">
      <label class="search-label">
        <span class="sr-only">Filter by name</span>
        <input
          type="search"
          class="search"
          placeholder="Filter by name…"
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

    {#if loading && orgs.length > 0}
      <p class="muted refresh-note" role="status">Refreshing…</p>
    {/if}

    {#if loading && orgs.length === 0}
      <p class="muted" role="status">Loading organizations…</p>
    {:else if error}
      <p class="err" role="alert">{error}</p>
    {:else if filtered.length === 0}
      <p class="muted">
        {orgs.length === 0
          ? "No organizations returned for this token."
          : "No organizations match your filter."}
      </p>
      {#if orgs.length === 0 && emptyHint}
        <p class="hint" role="note">{emptyHint}</p>
      {/if}
    {:else}
      <ul class="list">
        {#each filtered as o (o.login)}
          <li class="row">
            <span class="login">{o.login}</span>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</section>

<p class="back">
  <a href="#/">← Home</a>
</p>

<style>
  .orgs {
    max-width: 36rem;
  }
  .orgs h1 {
    margin: 0 0 1rem;
    font-size: 1.25rem;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: center;
    margin-bottom: 1rem;
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
  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
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
    margin: 0;
    padding: 0;
    border: 1px solid #ddd;
    border-radius: 8px;
    overflow: hidden;
  }
  .row {
    padding: 0.55rem 0.75rem;
    border-bottom: 1px solid #eee;
  }
  .row:last-child {
    border-bottom: none;
  }
  .login {
    font-family: ui-monospace, monospace;
    font-size: 0.95rem;
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
  .err {
    color: #a40000;
    margin: 0 0 0.75rem;
  }
  .back {
    margin-top: 1.5rem;
    font-size: 0.9rem;
  }
  .back a {
    color: #0969da;
  }
</style>
