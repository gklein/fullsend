import { describe, expect, it } from "vitest";
import { markdownToHtml } from "./markdown";

describe("markdownToHtml", () => {
  it("renders GFM table", async () => {
    const md = "|a|b|\n|-|-|\n|1|2|\n";
    const { html } = await markdownToHtml(md, "docs/test.md");
    expect(html).toContain("<table");
    expect(html).toContain("1");
  });

  it("rewrites relative md link to hash doc URL", async () => {
    const md = "[x](./other.md)";
    const { html } = await markdownToHtml(
      md,
      "docs/guides/admin/installation.md",
    );
    expect(html).toContain('href="#/guides/admin/other"');
  });

  it("strips front matter from HTML body", async () => {
    const md = "---\ntitle: Hello\n---\n\n# Body\n";
    const { html, frontmatter } = await markdownToHtml(md, "docs/x.md");
    expect(frontmatter.title).toBe("Hello");
    expect(html).toContain("Body");
    expect(html).not.toContain("Hello");
  });

  it("rewrites link with heading fragment to :: slug form", async () => {
    const md = "[z](./other.md#Section-One)";
    const { html } = await markdownToHtml(
      md,
      "docs/guides/admin/installation.md",
    );
    expect(html).toMatch(/href="#\/guides\/admin\/other::section-one"/);
  });

  it("marks mermaid fence for client render", async () => {
    const md = "```mermaid\nflowchart LR\n  A-->B\n```\n";
    const { html } = await markdownToHtml(md, "docs/a.md");
    expect(html).toContain('class="mermaid-doc"');
    expect(html).toContain("flowchart");
  });
});
