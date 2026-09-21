import { expect, test } from "@playwright/test";

async function mockProviderApp(page: import("@playwright/test").Page) {
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ json: { Theme: "light", Language: "de" } }),
  );
  await page.route("**/api/v1/boards", (route) => route.fulfill({ json: [] }));
  await page.route("**/events", (route) => route.abort());
  await page.route("**/api/v1/settings/providers", (route) =>
    route.fulfill({
      json: [
        { Provider: "codex", Command: "codex exec", SecretEnv: "", BaseURL: "", Options: "{}", Enabled: true },
        { Provider: "openai", Command: "", SecretEnv: "OPENAI_API_KEY", BaseURL: "", Options: "{}", Enabled: false },
        { Provider: "claude", Command: "claude", SecretEnv: "ANTHROPIC_API_KEY", BaseURL: "", Options: "{}", Enabled: false },
        {
          Provider: "grokbot",
          Command: "",
          SecretEnv: "XAI_API_KEY",
          BaseURL: "https://api.x.ai/v1",
          Options: '{"models":["grok-4"],"efforts":["low","medium","high"]}',
          Enabled: false,
        },
      ],
    }),
  );
  await page.route("**/api/v1/settings/capabilities", (route) =>
    route.fulfill({
      json: [
        { provider: "codex", models: ["gpt-5.6-luna"], efforts: ["low", "medium", "high"] },
        { provider: "grokbot", models: ["grok-4"], efforts: ["low", "medium", "high"], source: "Grokbot-Provideroptionen" },
      ],
    }),
  );
  await page.route("**/api/v1/skills", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/settings/sandbox-profiles", (route) =>
    route.fulfill({ json: [{ Name: "strict", Description: "isolated worktree" }] }),
  );
}

test.describe("grokbot provider", () => {
  test("shows grokbot in settings with xAI base URL and secret reference", async ({ page }) => {
    await mockProviderApp(page);
    await page.goto("/app/#/settings/providers");

    await expect(page.getByText("grokbot")).toBeVisible();
    await page.getByText("grokbot").click();
    await expect(page.locator('input[value="https://api.x.ai/v1"]')).toBeVisible();
    await expect(page.locator('input[value="XAI_API_KEY"]')).toBeVisible();
    await expect(page.getByText("HTTP-Adapter mit Base URL")).toBeVisible();
  });

  test("lets an agent select grokbot and a discovered model", async ({ page }) => {
    await mockProviderApp(page);
    await page.goto("/app/#/agents/new");

    await expect(page.getByLabel("Provider", { exact: true })).toBeVisible();
    await page.getByLabel("Provider", { exact: true }).selectOption("grokbot");
    await expect(page.getByRole("option", { name: "grok-4" })).toBeAttached();
    await page.getByLabel("Agent-Modell").selectOption("grok-4");
    await expect(page.getByLabel("Agent-Modell")).toHaveValue("grok-4");
  });
});
