import { expect, test, type Page } from "@playwright/test";

async function mockApp(page: Page, boards: Array<{ ID: string; Name: string }>, language = "de") {
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ json: { Theme: "light", Language: language } }),
  );
  await page.route("**/api/v1/boards", (route) => route.fulfill({ json: boards }));
  await page.route("**/events", (route) => route.abort());
}

test.describe("Sidebar navigation", () => {
  test("moves focus across the Boards button and its submenu", async ({ page }) => {
    await mockApp(page, [{ ID: "board-1", Name: "Plattform" }]);
    await page.goto("/app/#/projects");

    const projects = page.getByRole("link", { name: "Projekte" });
    const boardsToggle = page.getByRole("button", { name: "Boards" });
    await projects.focus();
    await page.keyboard.press("ArrowDown");
    await expect(boardsToggle).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(page.getByRole("link", { name: "Alle Boards" })).toBeFocused();
    await page.keyboard.press("End");
    await expect(page.getByRole("link", { name: "Einstellungen" })).toBeFocused();
  });

  test("shows the active board and supports keyboard collapse", async ({ page }) => {
    await mockApp(page, [
      { ID: "board-1", Name: "Plattform" },
      { ID: "board-2", Name: "Operations" },
    ]);
    await page.goto("/app/#/boards/board-2");

    const boardsToggle = page.getByRole("button", { name: "Boards" });
    const activeBoard = page.getByRole("link", { name: "Operations" });
    await expect(boardsToggle).toHaveAttribute("aria-expanded", "true");
    await expect(activeBoard).toHaveAttribute("aria-current", "page");

    await boardsToggle.focus();
    await page.keyboard.press("ArrowDown");
    await expect(page.locator('[data-nav-index="3"]')).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await expect(boardsToggle).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(boardsToggle).toHaveAttribute("aria-expanded", "false");
    await page.keyboard.press("Enter");
    await expect(boardsToggle).toHaveAttribute("aria-expanded", "true");
  });

  test("keeps board destinations reachable when the desktop sidebar is collapsed", async ({ page }) => {
    await mockApp(page, [{ ID: "board-1", Name: "Plattform" }]);
    await page.goto("/app/#/boards");

    await page.getByRole("button", { name: "Navigation öffnen oder schließen" }).click();
    await expect(page.getByRole("link", { name: "Plattform" })).toBeVisible();
    await page.getByRole("button", { name: "Boards" }).click();
    await expect(page.getByRole("link", { name: "Plattform" })).toBeHidden();
  });

  test("returns focus to the mobile menu button after Escape and overlay close", async ({ page }) => {
    await mockApp(page, [{ ID: "board-1", Name: "Plattform" }]);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/app/#/boards");

    const menuButton = page.getByRole("button", { name: "Navigation öffnen oder schließen" });
    await menuButton.click();
    await page.keyboard.press("Escape");
    await expect(menuButton).toBeFocused();
    await menuButton.click();
    await page.getByRole("button", { name: "Navigation schließen" }).click();
    await expect(menuButton).toBeFocused();
  });

  test("traps keyboard focus in the mobile drawer", async ({ page }) => {
    await mockApp(page, [{ ID: "board-1", Name: "Plattform" }]);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/app/#/boards");

    const menuButton = page.getByRole("button", { name: "Navigation öffnen oder schließen" });
    await menuButton.click();
    const drawer = page.getByRole("dialog", { name: "Shipyard" });
    await expect(drawer).toHaveAttribute("aria-modal", "true");
    await drawer.getByRole("link", { name: "Übersicht" }).focus();
    await page.keyboard.press("Shift+Tab");
    await expect(page.getByRole("button", { name: "Dunkel" })).toBeFocused();
  });

  test("localizes the boards navigation and supports long lists", async ({ page }) => {
    await mockApp(page, Array.from({ length: 80 }, (_, index) => ({ ID: `board-${index}`, Name: `Board ${index}` })), "en");
    await page.goto("/app/#/boards/board-79");

    await expect(page.getByRole("navigation", { name: "Main navigation" })).toBeVisible();
    await expect(page.getByRole("link", { name: "All boards" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Board 79" })).toHaveAttribute("aria-current", "page");
    await expect(page.locator(".board-subnavigation")).toHaveCSS("overflow-y", "auto");
    const boardMenu = page.locator(".board-subnavigation");
    await expect.poll(() => boardMenu.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
  });

  test("keeps the active board after direct navigation and reload", async ({ page }) => {
    await mockApp(page, [{ ID: "board-1", Name: "Plattform" }]);
    await page.goto("/app/#/boards/board-1");
    await expect(page.getByRole("link", { name: "Plattform" })).toHaveAttribute("aria-current", "page");
    await page.reload();
    await expect(page.getByRole("button", { name: "Boards" })).toHaveAttribute("aria-expanded", "true");
    await expect(page.getByRole("link", { name: "Plattform" })).toHaveAttribute("aria-current", "page");
  });

  test("keeps the sidebar usable at tablet width", async ({ page }) => {
    await mockApp(page, [{ ID: "board-1", Name: "Plattform" }]);
    await page.setViewportSize({ width: 900, height: 700 });
    await page.goto("/app/#/boards");
    await expect(page.getByRole("navigation", { name: "Hauptnavigation" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Plattform" })).toBeVisible();
  });

  test("renders an understandable empty state on mobile", async ({ page }) => {
    await mockApp(page, []);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/app/#/boards");

    await page.getByRole("button", { name: "Navigation öffnen oder schließen" }).click();
    await expect(page.getByRole("navigation", { name: "Hauptnavigation" })).toBeVisible();
    await expect(page.getByText("Noch keine Boards")).toBeVisible();
    await expect(page.getByRole("button", { name: "Boards" })).toHaveAttribute("aria-expanded", "true");
  });
});
