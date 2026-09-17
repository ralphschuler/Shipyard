import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import path from "node:path";

const apiFixtures: Record<string, unknown> = {
  "/api/v1/settings/appearance": { Theme: "light", Language: "de" },
  "/api/v1/projects": [{
    ID: "project-1",
    Name: "Shipyard",
    RepositoryURL: "https://github.com/ralphschuler/Shipyard.git",
    DefaultBranch: "master",
    Boards: [],
  }],
  "/api/v1/boards/board-1": {
    Board: { ID: "board-1", Name: "Board 1" },
    Labels: [],
    Projects: [],
    Groups: [],
    Columns: [{ ID: "column-1", Name: "Inbox" }],
    Tasks: [],
    Transitions: [],
  },
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
  "/api/v1/tasks/task-1": {
    Task: {
      ID: "task-1",
      Title: "Modal-Layout prüfen",
      Description: "Eine produktive Task-Ansicht mit Dialogen.",
      Priority: "high",
      BoardID: "board-1",
      Labels: [],
    },
    Interactions: [{
      ID: "interaction-1",
      Title: "Welche Oberfläche soll geprüft werden?",
      Body: "Bitte wähle eine Oberfläche.",
      Fields: [{
        ID: "surface",
        Label: "Oberfläche",
        Type: "select",
        Required: true,
        Options: [{ Value: "web", Label: "Web" }, { Value: "mobile", Label: "Mobile" }],
      }],
    }],
    Comments: [],
    Allowed: [],
    Columns: {},
    BoardLabels: [],
    Agents: [],
    Runs: [],
    History: [],
    Projects: [],
    Groups: [],
    TargetProjects: [],
    TargetGroups: [],
    Changes: [],
  },
  "/api/v1/runs/run-1": {
    run: { ID: "run-1", Status: "succeeded" },
    task: { ID: "task-1", Title: "Modal-Layout prüfen" },
    delivery: {
      GateStatus: "passed",
      DiffSummary: "1 Datei geändert",
      GateOutput: "Alle Prüfungen bestanden",
      AppliedAt: null,
    },
    usage: {},
  },
};
const legacyStyles = await readFile(path.resolve(import.meta.dirname, "../../internal/web/static/app.css"), "utf8");

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

test("React project modal remains bounded and scrolls its body at every supported viewport", async ({ page }) => {
  await mockReactBackend(page);
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1024, height: 600 },
    { width: 768, height: 480 },
    { width: 390, height: 320 },
    { width: 390, height: 220 },
  ]) {
    await page.setViewportSize(viewport);
    await page.goto("/app/#/projects");

    const trigger = page.getByRole("button", { name: "Projekt anlegen" });
    await expect(trigger).toBeVisible();
    await trigger.click();

    const dialog = page.getByRole("dialog", { name: "Neues Projekt" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel("Name")).toBeFocused();

    const surface = await dialog.evaluate((element) => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      const body = element.querySelector<HTMLElement>('[data-slot="dialog-body"]')!;
      const header = element.querySelector<HTMLElement>('[data-slot="dialog-header"]')!;
      const footer = element.querySelector<HTMLElement>('[data-slot="dialog-footer"]')!;
      body.scrollTop = body.scrollHeight;
      return {
        backgroundColor: style.backgroundColor,
        color: style.color,
        top: rect.top,
        bottom: rect.bottom,
        viewportHeight: window.innerHeight,
        overflowY: getComputedStyle(body).overflowY,
        bodyScrolls: body.scrollHeight > body.clientHeight,
        bodyAtEnd: body.scrollTop + body.clientHeight >= body.scrollHeight,
        headerVisible: header.getBoundingClientRect().top >= rect.top,
        footerVisible: footer.getBoundingClientRect().bottom <= rect.bottom,
      };
    });
    expect(surface.backgroundColor).not.toBe("rgba(0, 0, 0, 0)");
    expect(surface.color).not.toBe("rgba(0, 0, 0, 0)");
    expect(surface.top).toBeGreaterThanOrEqual(0);
    expect(surface.bottom).toBeLessThanOrEqual(surface.viewportHeight);
    expect(surface.overflowY).toBe("auto");
    expect(surface.bodyScrolls).toBe(true);
    expect(surface.bodyAtEnd).toBe(true);
    expect(surface.headerVisible).toBe(true);
    expect(surface.footerVisible).toBe(true);

    const overlay = page.locator('[data-slot="dialog-overlay"]');
    await expect(overlay).toBeVisible();
    expect(await overlay.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe("rgba(0, 0, 0, 0)");

    await page.keyboard.press("Tab");
    await expect(dialog.getByLabel("Repository-URL")).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  }
});

test("React project edit dialog keeps the same modal contract", async ({ page }) => {
  await mockReactBackend(page);
  await page.setViewportSize({ width: 768, height: 480 });
  await page.goto("/app/#/projects");

  const trigger = page.getByRole("button", { name: "Bearbeiten", exact: true });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Projekt bearbeiten" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Name")).toHaveValue("Shipyard");
  await expect(dialog.getByLabel("Name")).toBeFocused();
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

test("productive task edit dialog traps focus and keeps the backdrop inert", async ({ page }) => {
  await mockReactBackend(page);
  await page.setViewportSize({ width: 390, height: 240 });
  await page.goto("/app/#/tasks/task-1");

  const trigger = page.getByRole("button", { name: "Bearbeiten", exact: true });
  await expect(trigger).toBeVisible();
  await trigger.click();

  const dialog = page.getByRole("dialog", { name: "Aufgabe bearbeiten" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Titel")).toBeFocused();
  expect(await dialog.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe(
    "rgba(0, 0, 0, 0)",
  );

  for (let index = 0; index < 10; index++) {
    await page.keyboard.press("Tab");
    await expect(dialog).toContainText("Aufgabe bearbeiten");
    expect(await page.evaluate(() => {
      const active = document.activeElement;
      const dialog = document.querySelector('[role="dialog"]');
      return active === dialog || dialog?.contains(active);
    })).toBe(true);
  }
  for (let index = 0; index < 4; index++) {
    await page.keyboard.press("Shift+Tab");
    expect(await page.evaluate(() => document.activeElement?.closest('[role="dialog"]') !== null)).toBe(true);
  }

  let backdropClicked = false;
  await page.evaluate(() => {
    document.querySelector("main")?.addEventListener("click", () => {
      document.body.dataset.backdropClicked = "true";
    });
  });
  await page.mouse.click(2, 2);
  backdropClicked = (await page.locator("body").getAttribute("data-backdrop-clicked")) === "true";
  expect(backdropClicked).toBe(false);

  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
});

test("productive interaction and change-approval flows remain usable", async ({ page }) => {
  await mockReactBackend(page);
  let answerBody = "";
  await page.route("**/interactions/interaction-1/answer", async (route) => {
    answerBody = route.request().postData() || "";
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });
  await page.goto("/app/#/tasks/task-1");
  await page.getByLabel("Oberfläche").selectOption("web");
  await page.getByRole("button", { name: "Antwort speichern" }).click();
  expect(answerBody).toContain("surface=web");
  await expect(page.getByRole("button", { name: "Bearbeiten", exact: true })).toBeVisible();

  await page.route("**/runs/run-1/diff", (route) => route.fulfill({ status: 200, body: "+ modal" }));
  await page.goto("/app/#/runs/run-1");
  await page.getByRole("button", { name: "Änderungen übernehmen" }).click();
  const dialog = page.getByRole("dialog", { name: "Änderungen übernehmen?" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Bestätigen und übernehmen" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("legacy dialog enhancement keeps nested forms usable and restores focus", async ({ page }) => {
  await page.route("**/api/i18n", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ languages: { de: {}, en: {} } }) }),
  );
  await page.goto("/app/");
  await page.setViewportSize({ width: 390, height: 240 });
  await page.setContent(`
    <button id="trigger" type="button">Legacy öffnen</button>
    <dialog id="legacy" data-closable>
      <article>
        <header><h2>Legacy-Dialog</h2><button class="close" type="button" aria-label="Schließen">×</button></header>
        <form><label>Name<input autofocus></label><textarea rows="40"></textarea><footer><button>Speichern</button></footer></form>
        <form><button type="submit">Löschen</button></form>
      </article>
    </dialog>
  `);
  await page.addStyleTag({ content: legacyStyles });
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

  const legacyLayout = await dialog.evaluate((element) => {
    const body = element.querySelector<HTMLElement>(".dialog-body")!;
    const header = element.querySelector<HTMLElement>("header")!;
    const footer = element.querySelector<HTMLElement>("footer")!;
    body.scrollTop = body.scrollHeight;
    return {
      backgroundColor: getComputedStyle(element).backgroundColor,
      bodyOverflowY: getComputedStyle(body).overflowY,
      bodyScrolls: body.scrollHeight > body.clientHeight,
      headerVisible: header.getBoundingClientRect().top >= element.getBoundingClientRect().top,
      footerVisible: footer.getBoundingClientRect().bottom <= element.getBoundingClientRect().bottom,
    };
  });
  expect(legacyLayout.backgroundColor).not.toBe("rgba(0, 0, 0, 0)");
  expect(legacyLayout.bodyOverflowY).toBe("auto");
  expect(legacyLayout.bodyScrolls).toBe(true);
  expect(legacyLayout.headerVisible).toBe(true);
  expect(legacyLayout.footerVisible).toBe(true);

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

test("legacy modal traps focus, blocks the backdrop, and rejects Escape when not closable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 240 });
  await page.goto("/app/");
  await page.setContent(`
    <button id="outside" type="button">Außerhalb</button>
    <dialog id="legacy">
      <article>
        <header><h2>Nicht schließbar</h2></header>
        <form><label>Name<input autofocus></label><textarea rows="40"></textarea><footer><button>Speichern</button></footer></form>
      </article>
    </dialog>
  `);
  await page.addStyleTag({ content: legacyStyles });
  const appScript = await readFile(path.resolve(import.meta.dirname, "../../internal/web/static/app.js"), "utf8");
  await page.addScriptTag({ content: appScript });
  await page.locator("#outside").evaluate(() => {
    const outside = document.querySelector<HTMLButtonElement>("#outside")!;
    outside.addEventListener("click", () => outside.dataset.clicked = "true");
    const dialog = document.querySelector<HTMLDialogElement>("#legacy")!;
    dialog.showModal();
  });

  const dialog = page.locator("#legacy");
  await expect(dialog).toBeVisible();
  await expect(dialog.locator("input")).toBeFocused();
  const focusables = dialog.locator("input, textarea, button");
  for (let index = 0; index < 6; index++) {
    await page.keyboard.press("Tab");
    await expect(dialog).toContainText("Nicht schließbar");
    expect(await page.evaluate(() => {
      const active = document.activeElement;
      const dialog = document.querySelector("#legacy");
      return active === dialog || dialog?.contains(active) ? "legacy" : active?.tagName;
    })).toBe("legacy");
  }
  await expect(focusables.first()).toBeAttached();

  await page.keyboard.press("Escape");
  await expect(dialog).toBeVisible();
  await page.mouse.click(4, 4);
  expect(await page.locator("#outside").getAttribute("data-clicked")).toBeNull();

  await dialog.evaluate((element) => (element as HTMLDialogElement).close());
  await expect(dialog).toBeHidden();
});
