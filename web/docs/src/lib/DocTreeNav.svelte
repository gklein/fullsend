<script lang="ts">
  import type { ManifestNode } from "virtual:fullsend-docs";
  import DocTreeNav from "./DocTreeNav.svelte";
  import { navigateToRouteKey } from "./routing";

  interface Props {
    nodes: ManifestNode[];
    activeRouteKey: string;
  }

  let { nodes, activeRouteKey }: Props = $props();
</script>

<ul class="doc-tree-list" role="list">
  {#each nodes as node (node.type === "file" ? node.routeKey : `dir:${node.name}`)}
    <li class="doc-tree-item">
      {#if node.type === "dir"}
        <div class="doc-tree-dir">{node.name}</div>
        <DocTreeNav nodes={node.children} {activeRouteKey} />
      {:else}
        <button
          type="button"
          class="doc-tree-link"
          class:doc-tree-link--active={node.routeKey === activeRouteKey}
          onclick={() => navigateToRouteKey(node.routeKey)}
        >
          {node.title}
        </button>
      {/if}
    </li>
  {/each}
</ul>
