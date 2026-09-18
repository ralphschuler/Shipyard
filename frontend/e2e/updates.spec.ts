import { expect, test } from "@playwright/test";

test("renders a release changelog as safe markdown with a source fallback", async ({ page }) => {
  await page.route("**/events", (route) => route.abort());
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "de" }) }),
  );
  await page.route("**/api/v1/settings/updates", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({
      current: { version: "1.2.0", commit: "abc", builtAt: "" },
      source: { provider: "GitHub", repository: "acme/shipyard", branch: "master" },
      status: "update_available", installable: false, release: {
        version: "v1.3.0", commit: "def", publishedAt: "2026-09-17", verified: true, compatible: true,
        changelog: "# Verbesserungen\n\n- Schnellere Updates\n- [Release](https://github.com/acme/shipyard/releases)\n\n| Bereich | Status |\n| --- | --- |\n| UI | OK |\n\n```sh\necho sicher\n```\n\n<a href=\"javascript:alert(1)\">gefährlich</a>",
        url: "javascript:alert(1)",
      },
    }) }),
  );
  await page.goto("/app/#/settings/updates");

  await expect(page.getByRole("heading", { name: "Verbesserungen" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Release" })).toHaveAttribute("href", "https://github.com/acme/shipyard/releases");
  await expect(page.locator("table")).toContainText("UI");
  await expect(page.locator("pre")).toContainText("echo sicher");
  await expect(page.locator("a[href^='javascript:']")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Auf GitHub ansehen" })).toHaveCount(0);
  await page.getByRole("button", { name: "Quelltext anzeigen" }).click();
  await expect(page.getByRole("region", { name: "Changelog-Quelltext" })).toContainText("<a href=\"javascript");
});

test("shows raw changelog text when Markdown cannot be rendered", async ({ page }) => {
  await page.route("**/events", (route) => route.abort());
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "de" }) }),
  );
  await page.route("**/api/v1/settings/updates", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({
      current: { version: "1.2.0", commit: "abc", builtAt: "" },
      source: { provider: "GitHub", repository: "acme/shipyard", branch: "master" },
      status: "update_available", installable: false, release: {
        version: "v1.3.0", commit: "def", verified: true, compatible: true,
        changelog: "# Unvollständig\n\n```\nkein Abschluss",
      },
    }) }),
  );
  await page.goto("/app/#/settings/updates");

  await expect(page.getByRole("alert")).toContainText("konnte nicht formatiert werden");
  await expect(page.getByRole("alert")).toContainText("kein Abschluss");
});
