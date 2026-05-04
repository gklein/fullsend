import type { Plugin } from "vite";
import fs from "node:fs";
import path from "node:path";
import { listDocMarkdownFiles, filePathToRouteKey, type DocsFilePath } from "./paths";
import { markdownToHtml } from "./markdown";

const VIRTUAL_ID = "\0virtual:fullsend-docs";
const RESOLVED_VIRTUAL = "virtual:fullsend-docs";

export type ManifestNode =
  | { type: "dir"; name: string; children: ManifestNode[] }
  | { type: "file"; name: string; routeKey: string; title: string };

type FileNode = Extract<ManifestNode, { type: "file" }>;

/** Internal tree nodes use a `Map` for dirs; manifest output uses arrays. */
type DirNode = {
  children: Map<string, DirNode | FileNode>;
};

function buildTree(
  paths: { routeKey: string; title: string; segments: string[] }[],
): ManifestNode[] {
  const root: DirNode = { children: new Map() };

  function ensureDir(d: DirNode, name: string): DirNode {
    const existing = d.children.get(name);
    if (existing) {
      if ("routeKey" in existing) {
        throw new Error(`path conflict: ${name} is both file and directory`);
      }
      return existing;
    }
    const next: DirNode = { children: new Map() };
    d.children.set(name, next);
    return next;
  }

  for (const p of paths) {
    let d = root;
    const segs = p.segments;
    for (let i = 0; i < segs.length - 1; i++) {
      d = ensureDir(d, segs[i]!);
    }
    const leafName = segs[segs.length - 1]!;
    const fileNode: FileNode = {
      type: "file",
      name: leafName,
      routeKey: p.routeKey,
      title: p.title,
    };
    if (d.children.has(leafName)) {
      const existing = d.children.get(leafName);
      if (existing && !("routeKey" in existing)) {
        throw new Error(`path conflict: ${leafName} is both file and directory`);
      }
    }
    d.children.set(leafName, fileNode);
  }

  function toManifest(dir: DirNode): ManifestNode[] {
    const nodes: ManifestNode[] = [];
    for (const [name, ch] of [...dir.children.entries()].sort(([a], [b]) =>
      a.localeCompare(b),
    )) {
      if ("routeKey" in ch) {
        nodes.push(ch);
      } else {
        nodes.push({
          type: "dir",
          name,
          children: toManifest(ch),
        });
      }
    }
    return nodes;
  }

  return toManifest(root);
}

export function fullsendDocsPlugin(repoRoot: string): Plugin {
  return {
    name: "fullsend-docs",
    resolveId(id) {
      if (id === RESOLVED_VIRTUAL) return VIRTUAL_ID;
      return undefined;
    },
    load(id) {
      if (id !== VIRTUAL_ID) return undefined;
      return loadVirtualModule(repoRoot);
    },
    configureServer(server) {
      const docsDir = path.join(repoRoot, "docs");
      if (fs.existsSync(docsDir)) {
        server.watcher.add(docsDir);
      }
    },
  };
}

async function loadVirtualModule(repoRoot: string): Promise<string> {
  const files = listDocMarkdownFiles(repoRoot);
  const pages: Record<string, { title: string; html: string }> = {};
  const meta: { routeKey: string; title: string; segments: string[] }[] = [];

  for (const f of files) {
    const abs = path.join(repoRoot, f);
    const md = fs.readFileSync(abs, "utf8");
    const { title, html } = await markdownToHtml(md, f as DocsFilePath);
    const routeKey = filePathToRouteKey(f as DocsFilePath);
    pages[routeKey] = { title, html };
    meta.push({
      routeKey,
      title,
      segments: routeKey.split("/").filter(Boolean),
    });
  }

  meta.sort((a, b) => a.routeKey.localeCompare(b.routeKey));
  const manifest = buildTree(meta);

  return `export const manifest = ${JSON.stringify(manifest)};\nexport const pages = ${JSON.stringify(pages)};\n`;
}
