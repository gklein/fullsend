/// <reference types="svelte" />
/// <reference types="vite/client" />

declare module "virtual:fullsend-docs" {
  export type ManifestNode =
    | { type: "dir"; name: string; children: ManifestNode[] }
    | {
        type: "file";
        name: string;
        routeKey: string;
        title: string;
      };

  export const manifest: ManifestNode[];
  export const pages: Record<string, { title: string; html: string }>;
}
