import { describe, expect, it } from "vitest";
import { markdownToHtml } from "./markdown";

describe("markdownToHtml", () => {
  it("renders GFM table", async () => {
    const md = "|a|b|\n|-|-|\n|1|2|\n";
    const { html } = await markdownToHtml(md, "docs/test.md");
    expect(html).toContain("<table");
    expect(html).toContain("1");
  });

  it("rewrites relative md link to /docs/ URL", async () => {
    const md = "[x](./other.md)";
    const { html } = await markdownToHtml(
      md,
      "docs/guides/admin/installation.md",
    );
    expect(html).toContain('href="/docs/guides/admin/other"');
  });

  it("marks mermaid fence for client render", async () => {
    const md = "```mermaid\nflowchart LR\n  A-->B\n```\n";
    const { html } = await markdownToHtml(md, "docs/a.md");
    expect(html).toContain('class="mermaid-doc"');
    expect(html).toContain("flowchart");
  });
});
