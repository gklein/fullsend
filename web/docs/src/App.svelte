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
  let narrowViewport = $state(false);

  let outlineExpanded = $derived(
    narrowViewport ? mobileNavOpen : !navCollapsed,
  );

  let hamburgerLabel = $derived(
    !narrowViewport && !navCollapsed
      ? "Outline open"
      : narrowViewport && mobileNavOpen
        ? "Close documentation outline"
        : "Open documentation outline",
  );

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

    const mq = window.matchMedia("(max-width: 768px)");
    const syncNarrow = () => {
      narrowViewport = mq.matches;
    };
    syncNarrow();
    mq.addEventListener("change", syncNarrow);

    syncRouteFromLocation();

    const onHashOrPop = () => syncRouteFromLocation();
    window.addEventListener("hashchange", onHashOrPop);
    window.addEventListener("popstate", onHashOrPop);
    return () => {
      mq.removeEventListener("change", syncNarrow);
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

  function persistNavCollapsed(collapsed: boolean): void {
    navCollapsed = collapsed;
    localStorage.setItem(NAV_COLLAPSED_KEY, collapsed ? "1" : "0");
  }

  function onHamburgerClick(): void {
    if (narrowViewport) {
      mobileNavOpen = !mobileNavOpen;
      if (mobileNavOpen) {
        persistNavCollapsed(false);
      }
      return;
    }
    if (navCollapsed) {
      persistNavCollapsed(false);
    }
  }

  function closeOutlineDesktop(): void {
    persistNavCollapsed(true);
  }

  function closeOutlineMobile(): void {
    mobileNavOpen = false;
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
      class="docs-icon-btn docs-hamburger"
      aria-controls="docs-sidebar"
      aria-expanded={outlineExpanded}
      aria-label={hamburgerLabel}
      disabled={!narrowViewport && !navCollapsed}
      onclick={onHamburgerClick}
    >
      <svg width="20" height="20" viewBox="0 0 24 24" focusable="false" aria-hidden="true">
        <path
          fill="currentColor"
          d="M4 6h16v2H4V6zm0 5h16v2H4v-2zm0 5h16v2H4v-2z"
        />
      </svg>
    </button>
    <span class="docs-brand">Fullsend docs</span>
  </header>

  <div class="docs-layout">
    <aside class="docs-sidebar" id="docs-sidebar" aria-label="Documentation outline">
      <div class="docs-sidebar-header">
        <span class="docs-sidebar-title">Outline</span>
        <button
          type="button"
          class="docs-icon-btn docs-sidebar-close docs-sidebar-close--desktop"
          aria-label="Close outline"
          onclick={closeOutlineDesktop}
        >
          <svg width="18" height="18" viewBox="0 0 24 24" focusable="false" aria-hidden="true">
            <path
              fill="currentColor"
              d="M18.3 5.71 12 12l6.3 6.29-1.42 1.42L10.59 13.4 4.29 19.7 2.86 18.3 9.17 12 2.86 5.71 4.29 4.3l6.3 6.29 6.29-6.3z"
            />
          </svg>
        </button>
        <button
          type="button"
          class="docs-icon-btn docs-sidebar-close docs-sidebar-close--mobile"
          aria-label="Close outline"
          onclick={closeOutlineMobile}
        >
          <svg width="18" height="18" viewBox="0 0 24 24" focusable="false" aria-hidden="true">
            <path
              fill="currentColor"
              d="M18.3 5.71 12 12l6.3 6.29-1.42 1.42L10.59 13.4 4.29 19.7 2.86 18.3 9.17 12 2.86 5.71 4.29 4.3l6.3 6.29 6.29-6.3z"
            />
          </svg>
        </button>
      </div>
      <nav class="docs-tree-wrap">
        <DocTreeNav nodes={manifest} activeRouteKey={routeKey} />
      </nav>
    </aside>

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
  </div>
</div>
