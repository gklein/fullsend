<script lang="ts">
  import { onMount, tick } from "svelte";
  import mermaid from "mermaid";
  import { manifest, pages } from "virtual:fullsend-docs";
  import { routeKeyToUrl } from "./lib/docUrls";
  import {
    defaultRouteKey,
    getRouteKeyFromLocation,
  } from "./lib/routing";
  import { DocTreeNav } from "./lib/tree";

  const NAV_COLLAPSED_KEY = "fullsend-docs-nav-collapsed";

  function normalizeRouteKey(): string {
    let k = getRouteKeyFromLocation();
    if (!k || !pages[k]) {
      const d = defaultRouteKey(pages);
      if (!d) return "";
      const url = routeKeyToUrl(d);
      if (typeof window !== "undefined" && window.location.pathname !== url) {
        history.replaceState(null, "", url);
      }
      return d;
    }
    return k;
  }

  let routeKey = $state(normalizeRouteKey());
  let mermaidInit = $state(false);
  let navCollapsed = $state(false);
  let mobileNavOpen = $state(false);

  async function runMermaid(): Promise<void> {
    await tick();
    try {
      if (!document.querySelector(".doc-body pre.mermaid-doc")) return;
      await mermaid.run({ querySelector: ".doc-body pre.mermaid-doc" });
    } catch {
      /* empty graph or mermaid internal error — ignore */
    }
  }

  onMount(() => {
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
    mermaidInit = true;
    navCollapsed = localStorage.getItem(NAV_COLLAPSED_KEY) === "1";

    routeKey = normalizeRouteKey();

    const onPop = () => {
      routeKey = normalizeRouteKey();
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  });

  $effect(() => {
    if (!mermaidInit) return;
    const _ = routeKey;
    void runMermaid();
  });

  function toggleNavCollapsed(): void {
    navCollapsed = !navCollapsed;
    localStorage.setItem(NAV_COLLAPSED_KEY, navCollapsed ? "1" : "0");
  }

  function toggleMobileNav(): void {
    mobileNavOpen = !mobileNavOpen;
  }

  const pageHtml = $derived(pages[routeKey]?.html ?? "");
</script>

<div
  class="docs-shell"
  class:docs-shell--nav-collapsed={navCollapsed}
  class:docs-shell--mobile-nav-open={mobileNavOpen}
>
  <header class="docs-topbar">
    <button
      type="button"
      class="docs-mobile-nav-toggle"
      aria-controls="docs-sidebar"
      aria-expanded={mobileNavOpen}
      onclick={toggleMobileNav}
    >
      {mobileNavOpen ? "Close" : "Browse"}
    </button>
    <span class="docs-brand">Fullsend docs</span>
  </header>

  <div class="docs-layout">
    <main class="docs-main">
      {#if routeKey && pages[routeKey]}
        <article class="doc-body">
          {@html pageHtml}
        </article>
      {:else}
        <article class="doc-body doc-body--empty">
          <p>No documentation pages were found.</p>
        </article>
      {/if}
    </main>

    <aside class="docs-sidebar" id="docs-sidebar" aria-label="Documentation outline">
      <div class="docs-sidebar-header">
        <span class="docs-sidebar-title">Outline</span>
        <button
          type="button"
          class="docs-sidebar-collapse docs-sidebar-collapse--desktop"
          aria-expanded={!navCollapsed}
          onclick={toggleNavCollapsed}
        >
          {navCollapsed ? "Show" : "Hide"}
        </button>
        <button
          type="button"
          class="docs-sidebar-close docs-sidebar-close--mobile"
          onclick={() => {
            mobileNavOpen = false;
          }}
        >
          Close
        </button>
      </div>
      <nav class="docs-tree-wrap">
        <DocTreeNav nodes={manifest} activeRouteKey={routeKey} />
      </nav>
    </aside>
  </div>
</div>
