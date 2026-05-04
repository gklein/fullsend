import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkRehype from "remark-rehype";
import rehypeStringify from "rehype-stringify";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import { visit } from "unist-util-visit";
import path from "node:path";
import { toString } from "mdast-util-to-string";
import type { Root as MdastRoot } from "mdast";
import type { Element, Root as HastRoot } from "hast";
import { filePathToRouteKey, routeKeyToUrl, type DocsFilePath } from "./paths";

/** Derive display title: first heading text, else route last segment. */
export function extractTitle(mdast: MdastRoot, routeKey: string): string {
  let heading: string | null = null;
  visit(mdast, "heading", (node) => {
    if (heading === null && node.depth === 1) {
      heading = toString(node).trim();
    }
  });
  if (heading) return heading;
  const base = routeKey.split("/").pop() ?? routeKey;
  return base === "README"
    ? (routeKey.split("/").slice(-2, -1)[0] ?? "README")
    : base;
}

function remarkRewriteMdLinks(this: import("unified").Processor, sourceFile: DocsFilePath) {
  return (tree: MdastRoot) => {
    visit(tree, "link", (node) => {
      const url = node.url;
      if (!url || /^(https?:|mailto:|#)/i.test(url)) return;

      const [pathPart, frag] = url.split("#", 2);
      if (!pathPart.endsWith(".md") && !pathPart.endsWith(".markdown")) return;

      const dir = path.posix.dirname(filePathToRouteKey(sourceFile));
      const resolvedPosix = path.posix.normalize(
        path.posix.join(dir === "." ? "" : dir, pathPart),
      );
      const key = resolvedPosix.endsWith(".md")
        ? resolvedPosix.slice(0, -".md".length)
        : resolvedPosix.replace(/\.markdown$/, "");
      let href = routeKeyToUrl(key);
      if (frag) href += `#${frag}`;
      node.url = href;
    });
    return tree;
  };
}

/** After remark-rehype: turn `pre > code.language-mermaid` into Mermaid-friendly structure. */
function rehypeMermaidClass() {
  return (tree: HastRoot) => {
    visit(tree, "element", (node: Element) => {
      if (node.tagName !== "pre") return;
      const child = node.children[0] as Element | undefined;
      if (!child || child.tagName !== "code") return;
      const cls = Array.isArray(child.properties.className)
        ? child.properties.className
        : [];
      const lang = cls.find((c) => String(c).startsWith("language-"));
      if (lang !== "language-mermaid") return;
      node.properties.className = ["mermaid-doc"];
      const text = child.children[0];
      if (text && text.type === "text" && typeof text.value === "string") {
        node.children = [{ type: "text", value: text.value }];
      }
    });
    return tree;
  };
}

const sanitizeSchema = {
  ...defaultSchema,
  attributes: {
    ...defaultSchema.attributes,
    code: [
      ...(defaultSchema.attributes?.code ?? []),
      "className",
      "class",
    ],
    pre: [...(defaultSchema.attributes?.pre ?? []), "className", "class"],
    span: [...(defaultSchema.attributes?.span ?? []), "className", "class"],
  },
};

export async function markdownToHtml(
  markdown: string,
  sourceFile: DocsFilePath,
): Promise<{ title: string; html: string }> {
  const routeKey = filePathToRouteKey(sourceFile);
  const processor = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkRewriteMdLinks, sourceFile)
    .use(remarkRehype, { allowDangerousHtml: false })
    .use(rehypeMermaidClass)
    .use(rehypeSanitize, sanitizeSchema)
    .use(rehypeStringify);

  const file = await processor.process(markdown);
  const html = String(file);
  const mdast = unified().use(remarkParse).use(remarkGfm).parse(markdown) as MdastRoot;
  const title = extractTitle(mdast, routeKey);
  return { title, html };
}
