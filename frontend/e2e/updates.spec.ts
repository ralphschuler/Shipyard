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

const updateFixture = {
  current: { version: "1.0.0", commit: "current-sha", builtAt: "2026-09-17T12:00:00Z" },
  source: { provider: "GitHub", repository: "example/shipyard", branch: "master" },
  status: "up_to_date",
  release: {},
  checked_at: "2026-09-17T12:00:00Z",
};

test("manual update check shows failure and retry without duplicate requests", async ({ page }) => {
  let checks = 0;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/settings/appearance") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "en" }) });
      return;
    }
    if (path === "/api/v1/settings/updates") {
      checks += 1;
      if (checks === 3) {
        await route.abort("failed");
        return;
      }
      if (checks === 2) {
        await new Promise((resolve) => setTimeout(resolve, 150));
        await route.fulfill({ contentType: "application/json", body: JSON.stringify(updateFixture) });
        return;
      }
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(updateFixture) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: "[]" });
  });

  await page.goto("/app/#/settings/updates");
  const checkButton = page.getByRole("button", { name: "Check for updates now" });
  await expect(checkButton).toBeVisible();
  await checkButton.click();
  await expect(checkButton).toHaveAttribute("aria-busy", "false");
  await expect(page.getByRole("status")).toContainText("System is up to date");

  await page.route("**/api/v1/settings/updates", async (route) => {
    checks += 1;
    await new Promise((resolve) => setTimeout(resolve, 150));
    if (checks === 3) {
      await route.abort("failed");
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(updateFixture) });
  });
  await checkButton.click();
  await checkButton.click();
  await expect(page.getByRole("alert")).toContainText("update check", { timeout: 2_000 });
  expect(checks).toBeGreaterThanOrEqual(3);
  await expect(checkButton).toBeEnabled();
  const checksBeforeRetry = checks;
  await checkButton.click();
  await expect(page.getByRole("status")).toContainText("System is up to date");
  expect(checks).toBe(checksBeforeRetry + 1);
});

test("manual update check reports an available release", async ({ page }) => {
  const availableFixture = {
    ...updateFixture,
    status: "update_available",
    installable: false,
    release: { version: "1.1.0", commit: "latest-sha", url: "https://github.com/example/shipyard/releases/tag/v1.1.0" },
  };
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/settings/appearance") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "en" }) });
      return;
    }
    if (path === "/api/v1/settings/updates") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(availableFixture) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: "[]" });
  });

  await page.goto("/app/#/settings/updates");
  await expect(page.getByRole("status")).toContainText("Update available");
  await expect(page.getByText("1.1.0")).toBeVisible();
  await expect(page.getByLabel("Last checked")).toBeVisible();
});

test("manual update check treats malformed responses as a readable failure", async ({ page }) => {
  let checks = 0;
  const malformedFixture = {
    ...updateFixture,
    current: { ...updateFixture.current, builtAt: { unexpected: true } },
  };
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/settings/appearance") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "en" }) });
      return;
    }
    if (path === "/api/v1/settings/updates") {
      checks += 1;
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(checks === 1 ? updateFixture : malformedFixture) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: "[]" });
  });

  await page.goto("/app/#/settings/updates");
  await page.getByRole("button", { name: "Check for updates now" }).click();

  await expect(page.getByRole("alert")).toContainText("update check");
  await expect(page.getByRole("status")).toContainText("could not be completed");
  await expect(page.getByText("1.0.0")).toBeVisible();
});
