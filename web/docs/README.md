# Fullsend docs browser (Svelte SPA)

This directory is the **in-repo documentation browser**: a **Svelte 5 + Vite** app built as part of the shared root Vite config and served under **`/docs/`** in production.

## Local dev

From the repository root, **`npm run dev`** serves:

- **`/admin/`** — admin installation UI ([`../admin/README.md`](../admin/README.md))
- **`/docs/`** — this app

Design and behavior are described in the [docs browser design spec](../../docs/superpowers/specs/2026-05-04-docs-browser-design.md).

**`/api/*`** routes on the site Worker exist for the **admin** flow (OAuth, GitHub API proxy, etc.); the docs browser does not rely on them.
