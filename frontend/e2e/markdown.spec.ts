import { expect, test } from "@playwright/test";
import { parseMarkdown } from "../src/lib/markdown";

test("parses the supported task markdown blocks", () => {
  const document = parseMarkdown(
    "# Release notes\n\n- Safe link: [docs](https://example.com)\n- second item\n\n> Keep this context\n\n| Area | Status |\n| --- | --- |\n| UI | Ready |\n\n```ts\nconst answer = 42;\n```",
  );

  expect(document).toEqual([
    { type: "heading", level: 1, text: "Release notes" },
    {
      type: "list",
      ordered: false,
      items: [
        [{ text: "Safe link: " }, { text: "", link: { label: "docs", href: "https://example.com" } }],
        [{ text: "second item" }],
      ],
    },
    { type: "blockquote", text: "Keep this context" },
    { type: "table", headers: ["Area", "Status"], rows: [["UI", "Ready"]] },
    { type: "code", language: "ts", text: "const answer = 42;" },
  ]);
});

test("removes unsafe HTML and links while keeping plain text readable", () => {
  const document = parseMarkdown(
    '<script>alert("xss")</script>\n\n[run](javascript:alert(1)) and <img src=x onerror=alert(1)>',
  );

  expect(document).toEqual([
    { type: "paragraph", text: "alert(\"xss\")" },
    { type: "paragraph", text: "run and " },
  ]);
});

test("preserves ordered lists as ordered blocks", () => {
  expect(parseMarkdown("1. First\n2. Second")).toEqual([
    { type: "list", ordered: true, items: [[{ text: "First" }], [{ text: "Second" }]] },
  ]);
});
