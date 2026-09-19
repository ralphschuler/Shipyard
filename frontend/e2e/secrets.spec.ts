import { expect, test } from "@playwright/test";

test.describe("managed secrets", () => {
  test("shows secret metadata and agent assignments without exposing values", async ({ page }) => {
    await page.route("**/api/v1/settings/appearance", (route) =>
      route.fulfill({ json: { Theme: "light", Language: "de" } }),
    );
    await page.route("**/api/v1/boards", (route) => route.fulfill({ json: [] }));
    await page.route("**/events", (route) => route.abort());
    await page.route("**/api/v1/settings/secrets", (route) =>
      route.fulfill({
        json: [{ ID: "secret-1", Name: "OpenAI", EnvName: "OPENAI_API_KEY", Description: "Primary API access", Revoked: false, AgentIDs: ["agent-1"] }],
      }),
    );
    await page.route("**/api/v1/agents", (route) =>
      route.fulfill({ json: [{ ID: "agent-1", Name: "Builder" }, { ID: "agent-2", Name: "Reviewer" }] }),
    );

    await page.goto("/app/#/settings/secrets");

    await expect(page.getByRole("link", { name: "Secrets" })).toBeVisible();
    await expect(page.getByText("OpenAI", { exact: true })).toBeVisible();
    await expect(page.getByText("OPENAI_API_KEY")).toBeVisible();
    await expect(page.getByText("Builder")).toBeVisible();
    await expect(page.getByText("Primary API access")).toBeVisible();
    await expect(page.getByText("OpenAI API key value")).toHaveCount(0);
    await expect(page.locator("body")).not.toContainText("super-secret-value");
  });

  test("submits only a new value for replacement and saves selected agents", async ({ page }) => {
    await page.route("**/api/v1/settings/appearance", (route) => route.fulfill({ json: { Theme: "light", Language: "de" } }));
    await page.route("**/api/v1/boards", (route) => route.fulfill({ json: [] }));
    await page.route("**/events", (route) => route.abort());
    await page.route("**/api/v1/settings/secrets", (route) => route.fulfill({ json: [{ ID: "secret-1", Name: "OpenAI", EnvName: "OPENAI_API_KEY", Description: "Primary API access", Revoked: false, AgentIDs: ["agent-1"] }] }));
    await page.route("**/api/v1/agents", (route) => route.fulfill({ json: [{ ID: "agent-1", Name: "Builder" }, { ID: "agent-2", Name: "Reviewer" }] }));
    let submitted = "";
    await page.route("**/settings/secrets/secret-1/replace", async (route) => {
      submitted = (await route.request().postData()) || "";
      await route.fulfill({ status: 303, headers: { location: "/settings/secrets" } });
    });
    await page.route("**/settings/secrets/secret-1/agents", async (route) => route.fulfill({ status: 303, headers: { location: "/settings/secrets" } }));

    await page.goto("/app/#/settings/secrets");
    await page.getByRole("button", { name: "OpenAI ersetzen" }).click();
    await page.getByLabel("Neuer Wert").fill("super-secret-value");
    await page.getByRole("button", { name: "Wert ersetzen" }).click();
    await expect.poll(() => submitted).toContain("value=super-secret-value");
    expect([...new URLSearchParams(submitted).keys()]).toEqual(["value"]);
    await expect(page.locator("body")).not.toContainText("super-secret-value");
  });
});
