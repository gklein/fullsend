<script lang="ts">
  import { manifest, pages } from "virtual:fullsend-docs";

  const pageCount = Object.keys(pages).length;
  const topLevelEntries = manifest.length;
</script>

<main>
  <p>Docs app shell (Task 6)</p>
  <p>
    {pageCount} markdown page(s), {topLevelEntries} top-level tree entr{topLevelEntries === 1
      ? "y"
      : "ies"}
  </p>
</main>
