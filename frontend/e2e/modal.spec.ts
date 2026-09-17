import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import path from "node:path";

const apiFixtures: Record<string, unknown> = {
  "/api/v1/settings/appearance": { Theme: "light", Language: "de" },
  "/api/v1/projects": [],
  "/api/v1/boards": Array.from({ length: 18 }, (_, index) => ({
    ID: `board-${index}`,
    Name: `Board ${index}`,
  })),
  "/api/v1/project-groups": Array.from({ length: 12 }, (_, index) => ({
    ID: `group-${index}`,
    Name: `Gruppe ${index}`,
    Projects: [],
  })),
  "/api/v1/boards/board-filter": {
    Board: { ID: "board-filter", Name: "Produkt" },
    Columns: [
      { ID: "todo", Name: "Offen" },
      { ID: "done", Name: "Erledigt" },
    ],
    Labels: [{ ID: "bug", Name: "Fehler" }, { ID: "ux", Name: "UX" }],
    Projects: [{ ID: "project-a", Name: "Website" }],
    Tasks: [
      { ID: "task-a", BoardID: "board-filter", ColumnID: "todo", Title: "Login reparieren", Description: "Fehler im Formular", Priority: "high", Labels: [{ ID: "bug", Name: "Fehler" }], TargetProjects: [{ ID: "project-a", Name: "Website" }] },
      { ID: "task-b", BoardID: "board-filter", ColumnID: "done", Title: "UX prüfen", Description: "Mobile Navigation", Priority: "normal", Labels: [{ ID: "ux", Name: "UX" }], TargetProjects: [{ ID: "project-a", Name: "Website" }] },
      { ID: "task-other", BoardID: "other-board", ColumnID: "todo", Title: "Login reparieren", Description: "Nicht dieses Board", Priority: "urgent", Labels: [], TargetProjects: [] },
    ],
    Transitions: [],
    Groups: [],
  },
  "/api/v1/boards/other-board": {
    Board: { ID: "other-board", Name: "Andere Ansicht" },
    Columns: [{ ID: "todo", Name: "Offen" }],
    Labels: [],
    Projects: [],
    Tasks: [],
    Transitions: [],
    Groups: [],
  },
};

async function mockReactBackend(page: Page) {
  await page.route("**/events", (route) => route.abort());
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const fixture = apiFixtures[url.pathname];
    await route.fulfill({
      status: fixture === undefined ? 404 : 200,
      contentType: "application/json",
      body: JSON.stringify(fixture ?? { error: "fixture missing" }),
    });
  });
}

test("React project modal scrolls, focuses, closes on Escape, and restores focus", async ({ page }) => {
  await mockReactBackend(page);
  await page.setViewportSize({ width: 390, height: 320 });
  await page.goto("/app/#/projects");

  const trigger = page.getByRole("button", { name: "Projekt anlegen" });
  await expect(trigger).toBeVisible();
  await trigger.click();

  const dialog = page.getByRole("dialog", { name: "Neues Projekt" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Name")).toBeFocused();

  const scrollState = await dialog.locator('[data-slot="dialog-body"]').evaluate((element) => ({
    overflowY: getComputedStyle(element).overflowY,
    scrollHeight: element.scrollHeight,
    clientHeight: element.clientHeight,
  }));
  expect(scrollState.overflowY).toBe("auto");
  expect(scrollState.scrollHeight).toBeGreaterThan(scrollState.clientHeight);

  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
});

test("React board filters search and combine status, priority, label, and project", async ({ page }) => {
  await mockReactBackend(page);
  await page.goto("/app/#/boards/board-filter");

  await expect(page.getByRole("heading", { name: "Produkt" })).toBeVisible();
  const search = page.getByRole("searchbox", { name: "Aufgaben suchen" });
  await search.fill("login");
  await expect(page.getByText("1 Aufgabe gefunden")).toBeVisible();
  await expect(page.getByText("Login reparieren")).toBeVisible();
  await expect(page.getByText("UX prüfen")).toBeHidden();

  await page.getByLabel("Priorität").selectOption("high");
  await page.getByLabel("Spalte").selectOption("todo");
  await page.getByLabel("Tag").selectOption("bug");
  await page.getByLabel("Projekt").selectOption("project-a");
  await expect(page.getByText("1 Aufgabe gefunden")).toBeVisible();
  await expect(page.getByRole("button", { name: /Fehler zurücksetzen/ })).toBeVisible();

  await page.getByRole("button", { name: "Alle Filter zurücksetzen" }).click();
  await expect(page.getByText("2 Aufgaben gefunden")).toBeVisible();
  await expect(page.getByText("task-other")).toHaveCount(0);

  await search.fill("mobile navigation");
  await expect(page.getByText("1 Aufgabe gefunden")).toBeVisible();
  await expect(page.getByText("UX prüfen")).toBeVisible();
  await expect(page.getByText("Login reparieren")).toBeHidden();
  await search.fill("login");
  await page.getByLabel("Projekt").selectOption("project-a");
  await expect(page.getByText("2 Aufgaben gefunden")).toBeVisible();
  await page.getByRole("button", { name: /Projekt: Website zurücksetzen/ }).click();
  await expect(page.getByText("2 Aufgaben gefunden")).toBeVisible();
});

test("React board filters keep each board's state isolated", async ({ page }) => {
  await mockReactBackend(page);
  await page.goto("/app/#/boards/board-filter");

  const search = page.getByRole("searchbox", { name: "Aufgaben suchen" });
  await search.fill("login");
  await expect(page.getByText("1 Aufgabe gefunden")).toBeVisible();

  await page.goto("/app/#/boards/other-board");
  await expect(page.getByRole("heading", { name: "Produkt" })).toBeHidden();
  await expect(search).toBeHidden();

  await page.goto("/app/#/boards/board-filter");
  await expect(page.getByRole("searchbox", { name: "Aufgaben suchen" })).toHaveValue("login");
  await expect(page.getByText("1 Aufgabe gefunden")).toBeVisible();
});

test("legacy dialog enhancement keeps nested forms usable and restores focus", async ({ page }) => {
  await page.route("**/api/i18n", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ languages: { de: {}, en: {} } }) }),
  );
  await page.goto("/app/");
  await page.setContent(`
    <button id="trigger" type="button">Legacy öffnen</button>
    <dialog id="legacy" data-closable>
      <article>
        <header><h2>Legacy-Dialog</h2><button class="close" type="button" aria-label="Schließen">×</button></header>
        <form><label>Name<input autofocus></label><textarea rows="20"></textarea><footer><button>Speichern</button></footer></form>
        <form><button type="submit">Löschen</button></form>
      </article>
    </dialog>
  `);
  const appScript = await readFile(path.resolve(import.meta.dirname, "../../internal/web/static/app.js"), "utf8");
  await page.addScriptTag({ content: appScript });
  await page.locator("#trigger").evaluate(() => {
    const trigger = document.querySelector<HTMLButtonElement>("#trigger")!;
    const dialog = document.querySelector<HTMLDialogElement>("#legacy")!;
    trigger.addEventListener("click", () => dialog.showModal());
  });
  await page.locator("#trigger").click();

  const dialog = page.locator("#legacy");
  await expect(dialog).toBeVisible();
  await expect(dialog.locator("input[autofocus]")).toBeFocused();
  await expect(dialog.locator(".dialog-body")).toHaveCount(1);
  await expect(dialog.locator("form.dialog-secondary-form")).toHaveCount(1);

  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(page.locator("#trigger")).toBeFocused();
});

function taskFixture(commentCount: number) {
  return {
    Task: { ID: `task-${commentCount}`, BoardID: "board-1", Title: "Kommentarprüfung", Description: "Beschreibung" },
    Allowed: [],
    Columns: {},
    Interactions: [],
    Changes: [],
    History: [],
    Agents: [],
    Runs: [],
    BoardLabels: [],
    Projects: [],
    TargetProjects: [],
    Groups: [],
    TargetGroups: [],
    Comments: Array.from({ length: commentCount }, (_, index) => ({
      ID: `comment-${index}`,
      Author: `Autor ${index + 1}`,
      CreatedAt: `2026-09-17T10:${String(index).padStart(2, "0")}:00Z`,
      Body: index === commentCount - 1
        ? `Ein sehr langer Kommentar, der vollständig lesbar bleiben muss. ${"Zusätzlicher Inhalt. ".repeat(20)}`
        : `Kommentar ${index + 1}`,
    })),
  };
}

test("task comments stay compact and reveal older comments without reloading", async ({ page }) => {
  await page.route("**/events", (route) => route.abort());
  await page.route("**/api/v1/tasks/task-*", async (route) => {
    const count = Number(new URL(route.request().url()).pathname.split("-").at(-1));
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(taskFixture(count)) });
  });
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "de" }) }),
  );
  await page.goto("/app/#/tasks/task-10");

  const comments = page.locator('[data-testid="task-comment"]');
  await expect(comments).toHaveCount(6);
  await expect(comments.nth(0)).toContainText("Kommentar 5");
  await expect(comments.nth(5)).toContainText("Ein sehr langer Kommentar");
  const longComment = comments.nth(5).locator("details");
  await expect(longComment).toHaveCount(1);
  await expect(longComment.locator("summary")).toContainText("Ein sehr langer Kommentar");
  await longComment.locator("summary").click();
  await expect(longComment.locator("p")).toContainText("Zusätzlicher Inhalt");
  await expect(page.getByRole("button", { name: "Ältere Kommentare anzeigen" })).toBeVisible();
  await expect(comments.nth(2)).toHaveAttribute("data-prominent", "false");
  await expect(comments.nth(3)).toHaveAttribute("data-prominent", "true");
  await expect(comments.nth(5)).toHaveAttribute("data-prominent", "true");

  const olderCommentsButton = page.getByRole("button", { name: "Ältere Kommentare anzeigen" });
  await olderCommentsButton.focus();
  const anchorTopBeforeReveal = await comments.nth(0).evaluate((element) => {
    return element.getBoundingClientRect().top;
  });
  await page.keyboard.press("Enter");
  await expect(comments).toHaveCount(10);
  const anchorTopAfterReveal = await comments.nth(4).evaluate((element) => element.getBoundingClientRect().top);
  expect(Math.abs(anchorTopAfterReveal - anchorTopBeforeReveal)).toBeLessThan(2);
  await expect(page.getByRole("button", { name: "Ältere Kommentare ausblenden" })).toHaveAttribute("aria-expanded", "true");
  await expect(comments.nth(0)).toContainText("Kommentar 1");
  await expect(comments.nth(9)).toContainText("Ein sehr langer Kommentar");
  await expect(comments.nth(6)).toHaveAttribute("data-prominent", "false");
  await expect(comments.nth(7)).toHaveAttribute("data-prominent", "true");

  await page.getByRole("button", { name: "Ältere Kommentare ausblenden" }).click();
  await expect(comments).toHaveCount(6);
  const anchorTopAfterCollapse = await comments.nth(0).evaluate((element) => element.getBoundingClientRect().top);
  expect(Math.abs(anchorTopAfterCollapse - anchorTopBeforeReveal)).toBeLessThan(2);
});

test.describe("comment thresholds", () => {
  for (const count of [0, 3, 6, 7]) {
    test(`${count} comments has the expected initial visibility`, async ({ page }) => {
      await page.route("**/events", (route) => route.abort());
      await page.route("**/api/v1/settings/appearance", (route) =>
        route.fulfill({ contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "de" }) }),
      );
      await page.route(`**/api/v1/tasks/task-${count}`, (route) =>
        route.fulfill({ contentType: "application/json", body: JSON.stringify(taskFixture(count)) }),
      );
      await page.goto(`/app/#/tasks/task-${count}`);
      await expect(page.locator('[data-testid="task-comment"]')).toHaveCount(Math.min(count, 6));
      await expect(page.getByRole("button", { name: "Ältere Kommentare anzeigen" })).toHaveCount(count > 6 ? 1 : 0);
    });
  }
});
