import { expect, test, type Page } from "@playwright/test";

async function mockApp(page: Page, boards: Array<{ ID: string; Name: string }>) {
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ json: { Theme: "light", Language: "de" } }),
  );
  await page.route("**/api/v1/boards", (route) => route.fulfill({ json: boards }));
  await page.route("**/events", (route) => route.abort());
}

test.describe("Sidebar navigation", () => {
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
    await page.keyboard.press("Enter");
    await expect(boardsToggle).toHaveAttribute("aria-expanded", "false");
    await page.keyboard.press("Enter");
    await expect(boardsToggle).toHaveAttribute("aria-expanded", "true");
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
