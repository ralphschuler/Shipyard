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
