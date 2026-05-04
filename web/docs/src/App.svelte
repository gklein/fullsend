<script lang="ts">
  import { onMount, tick } from "svelte";
  import type { DocPagePayload } from "virtual:fullsend-docs";
  import { manifest, loadPage } from "virtual:fullsend-docs";
  import { collectRouteKeys } from "./lib/manifestRouteKeys";
  import {
    defaultRouteKeyFromKeys,
    getDocRouteFromWindow,
    legacyPathnameDocRest,
    navigateToRouteKey,
  } from "./lib/routing";
  import { formatDocHash } from "./lib/hashRoute";
  import { DocTreeNav } from "./lib/tree";

  const NAV_COLLAPSED_KEY = "fullsend-docs-nav-collapsed";

  const routeKeys = new Set(collectRouteKeys(manifest));

  let routeKey = $state("");
  let slug = $state<string | undefined>(undefined);
  let page = $state<DocPagePayload | null>(null);
  let loading = $state(false);
  let navCollapsed = $state(false);
  let mobileNavOpen = $state(false);

  function syncRouteFromLocation(): void {
    const legacy = legacyPathnameDocRest();
    if (legacy !== null) {
      const u = new URL(window.location.href);
      u.pathname = "/docs/";
      u.hash = formatDocHash(legacy);
      location.replace(u.toString());
      return;
    }

    const parsed = getDocRouteFromWindow();
    const defaultKey = defaultRouteKeyFromKeys([...routeKeys]);

    if (routeKeys.size === 0) {
      routeKey = "";
      slug = undefined;
      return;
    }

    if (parsed === null) {
      if (defaultKey !== null) {
        navigateToRouteKey(defaultKey, { replace: true });
        routeKey = defaultKey;
        slug = undefined;
      }
      return;
    }

    if (!routeKeys.has(parsed.routeKey)) {
      if (defaultKey !== null) {
        navigateToRouteKey(defaultKey, { replace: true });
        routeKey = defaultKey;
        slug = undefined;
      }
      return;
    }

    routeKey = parsed.routeKey;
    slug = parsed.slug;
  }

  async function runMermaid(): Promise<void> {
    await tick();
    try {
      if (!document.querySelector(".doc-body pre.mermaid-doc")) return;
      const m = await import("mermaid");
      m.default.initialize({ startOnLoad: false, securityLevel: "strict" });
      await m.default.run({ querySelector: ".doc-body pre.mermaid-doc" });
    } catch {
      /* empty graph or mermaid internal error — ignore */
    }
  }

  onMount(() => {
    navCollapsed = localStorage.getItem(NAV_COLLAPSED_KEY) === "1";

    syncRouteFromLocation();

    const onHashOrPop = () => syncRouteFromLocation();
    window.addEventListener("hashchange", onHashOrPop);
    window.addEventListener("popstate", onHashOrPop);
    return () => {
      window.removeEventListener("hashchange", onHashOrPop);
      window.removeEventListener("popstate", onHashOrPop);
    };
  });

  $effect(() => {
    const key = routeKey;
    if (!key || !routeKeys.has(key)) {
      page = null;
      loading = false;
      return;
    }

    loading = true;

    void loadPage(key)
      .then((p) => {
        if (routeKey === key) {
          page = p;
        }
      })
      .catch(() => {
        if (routeKey === key) {
          page = null;
          const dk = defaultRouteKeyFromKeys([...routeKeys]);
          if (dk !== null) {
            navigateToRouteKey(dk, { replace: true });
          }
        }
      })
      .finally(() => {
        if (routeKey === key) {
          loading = false;
        }
      });
  });

  $effect(() => {
    if (!page?.html) return;
    void runMermaid();
  });

  $effect(() => {
    const s = slug;
    const html = page?.html;
    if (!s || !html) return;
    void tick().then(() => {
      const body = document.querySelector(".doc-body");
      if (!body) return;
      const el = document.getElementById(s);
      if (el && body.contains(el)) {
        el.scrollIntoView();
      }
    });
  });

  function toggleNavCollapsed(): void {
    navCollapsed = !navCollapsed;
    localStorage.setItem(NAV_COLLAPSED_KEY, navCollapsed ? "1" : "0");
  }

  function toggleMobileNav(): void {
    mobileNavOpen = !mobileNavOpen;
  }
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
      {#if routeKey && page}
        <article
          class="doc-body"
          data-frontmatter={JSON.stringify(page.frontmatter)}
        >
          {@html page.html}
        </article>
      {:else if routeKey && loading}
        <article class="doc-body doc-body--empty">
          <p>Loading…</p>
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
