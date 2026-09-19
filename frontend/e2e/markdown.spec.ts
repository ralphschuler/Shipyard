import { expect, test, type Page } from "@playwright/test";
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

const taskMarkdown = [
  "# Release notes",
  "",
  "A paragraph with a [safe link](https://example.com).",
  "",
  "- first item",
  "- second item",
  "",
  "> Keep this context",
  "",
  "| Area | Status |",
  "| --- | --- |",
  "| UI | Ready |",
  "",
  "```ts",
  "const veryLongLine = '" + "x".repeat(180) + "';",
  "```",
].join("\n");

const taskFixture = {
  Task: { ID: "task-markdown", BoardID: "board-1", Title: "Markdown task", Description: taskMarkdown, Priority: "normal" },
  Allowed: [],
  Interactions: [],
  Comments: [{ ID: "comment-1", Author: "Reviewer", Body: taskMarkdown, CreatedAt: "2026-09-17T10:00:00Z" }],
  History: [],
  Columns: [],
  Agents: [],
  Runs: [],
  BoardLabels: [],
  Projects: [],
  Groups: [],
  TargetProjects: [],
  TargetGroups: [],
  Changes: [],
};

const boardFixture = {
  ID: "board-1",
  Name: "Markdown board",
  Labels: [],
  Projects: [],
  Groups: [],
  Columns: [],
  Tasks: [],
};

async function mockTaskBackend(page: Page) {
  await page.route("**/events", (route) => route.abort());
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const fixture = url.pathname.endsWith("/settings/appearance")
      ? { Theme: "light", Language: "de" }
      : url.pathname.endsWith("/tasks/task-markdown")
        ? taskFixture
        : url.pathname.endsWith("/boards/board-1")
          ? boardFixture
          : {};
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(fixture) });
  });
}

test("renders stored markdown, comments, and the task editor preview consistently", async ({ page }) => {
  await mockTaskBackend(page);
  await page.goto("/app/#/tasks/task-markdown");

  const main = page.locator("main");
  await expect(main.getByRole("heading", { name: "Release notes", level: 1 })).toHaveCount(2);
  await expect(main.locator("table")).toHaveCount(2);
  await expect(main.locator("pre code")).toHaveCount(2);

  await page.getByRole("button", { name: "Bearbeiten", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Aufgabe bearbeiten" });
  await dialog.getByRole("tab", { name: "Vorschau" }).click();
  await expect(dialog.getByRole("tabpanel", { name: "Markdown-Vorschau" }).getByRole("heading", { name: "Release notes", level: 1 })).toBeVisible();
  await expect(dialog.locator("table")).toHaveCount(1);
  await expect(dialog.locator("pre code")).toHaveCount(1);
  await dialog.getByRole("tab", { name: "Markdown" }).click();
  await expect(dialog.getByRole("textbox", { name: "Beschreibung als Markdown" })).toHaveValue(taskMarkdown);
});

test("offers the same safe markdown preview when creating a task", async ({ page }) => {
  await mockTaskBackend(page);
  await page.goto("/app/#/boards/board-1");
  await page.getByRole("button", { name: "Aufgabe anlegen" }).click();
  const dialog = page.getByRole("dialog", { name: "Neue Aufgabe" });
  const description = dialog.getByRole("textbox", { name: "Beschreibung als Markdown" });
  await description.fill("<script>alert(1)</script>\n\n[run](javascript:alert(1))");
  await dialog.getByRole("tab", { name: "Vorschau" }).click();
  const preview = dialog.getByRole("tabpanel", { name: "Markdown-Vorschau" });
  await expect(preview).toContainText("alert(1)");
  await expect(preview.locator("script")).toHaveCount(0);
  await expect(preview.locator("a")).toHaveCount(0);
});

test("keeps long markdown code usable on a narrow viewport", async ({ page }) => {
  await mockTaskBackend(page);
  await page.setViewportSize({ width: 390, height: 700 });
  await page.goto("/app/#/tasks/task-markdown");
  const code = page.locator("main pre").first();
  const metrics = await code.evaluate((element) => ({
    overflowX: getComputedStyle(element).overflowX,
    scrollWidth: element.scrollWidth,
    clientWidth: element.clientWidth,
  }));
  expect(metrics.overflowX).toBe("auto");
  expect(metrics.scrollWidth).toBeGreaterThan(metrics.clientWidth);
});

test("preserves ordered lists as ordered blocks", () => {
  expect(parseMarkdown("1. First\n2. Second")).toEqual([
    { type: "list", ordered: true, items: [[{ text: "First" }], [{ text: "Second" }]] },
  ]);
});
