import { expect, test } from "@playwright/test";

async function mockLocaleApp(page: import("@playwright/test").Page, language: "de" | "en") {
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ json: { Theme: "light", Language: language } }),
  );
  await page.route("**/api/v1/boards", (route) => route.fulfill({ json: [] }));
  await page.route("**/events", (route) => route.abort());
}

test.describe("locale persistence and first render", () => {
  test("uses the account language before the first application paint", async ({ page }) => {
    await mockLocaleApp(page, "en");
    await page.goto("/app/#/projects");
    await expect(page.getByRole("navigation", { name: "Main navigation" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Projects" })).toBeVisible();
    await expect(page.getByText("Projekte")).toHaveCount(0);
  });

  test("persists a switch across reload and keeps all shell regions aligned", async ({ page }) => {
    await mockLocaleApp(page, "de");
    await page.goto("/app/#/settings");
    await page.evaluate(() => localStorage.setItem("shipyard-language", "en"));
    await page.reload();
    await expect(page.getByRole("navigation", { name: "Main navigation" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Projects" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.getByText("Einstellungen")).toHaveCount(0);
  });

  test("mounts promptly when the appearance endpoint is unavailable", async ({ page }) => {
    await page.route("**/api/v1/settings/appearance", (route) => route.abort());
    await page.route("**/api/v1/boards", (route) => route.fulfill({ json: [] }));
    await page.route("**/events", (route) => route.abort());
    await page.goto("/app/#/projects");
    await expect(page.getByRole("navigation", { name: /navigation/i })).toBeVisible({ timeout: 5000 });
  });
});
