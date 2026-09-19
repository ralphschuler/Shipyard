import { expect, test } from "@playwright/test";

const taskFixture = {
  Task: { ID: "task-tabs", BoardID: "board-1", Title: "Task tabs", Description: "A task description.", Priority: "high" },
  Allowed: [],
  Interactions: [],
  Comments: [],
  History: [],
  Columns: [],
  Agents: [{ ID: "agent-1", Name: "Delivery Agent", Enabled: true }],
  Runs: [],
  BoardLabels: [],
  Projects: [],
  Groups: [],
  TargetProjects: [],
  TargetGroups: [],
  Changes: [{ ID: "run-1", Status: "succeeded", GateStatus: "passed", DiffSummary: "1 file changed" }],
};

test.beforeEach(async ({ page }) => {
  await page.route("**/events", (route) => route.abort());
  await page.route("**/api/v1/settings/appearance", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ Theme: "light", Language: "de" }) }),
  );
  await page.route("**/api/v1/boards", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) }),
  );
  await page.route("**/api/v1/tasks/task-tabs", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(taskFixture) }),
  );
  await page.route("**/runs/run-1/diff", (route) =>
    route.fulfill({ status: 200, contentType: "text/plain", body: "diff --git a/README.md b/README.md\n+added\n-removed\n context" }),
  );
});

test("keeps task tabs above the title and switches between conversation and full-width changes", async ({ page }) => {
  await page.goto("/app/#/tasks/task-tabs?tab=changes");

  const main = page.locator("main");
  await expect(main.getByRole("tab", { name: "Changes" })).toHaveAttribute("aria-selected", "true");
  await expect(main.getByRole("heading", { name: "Task tabs", level: 2 })).toBeVisible();
  await expect(main.getByText("Nächster Schritt")).toHaveCount(0);
  await expect(main.getByText("README.md").last()).toBeVisible();

  await main.getByRole("tab", { name: "Conversation" }).click();
  await expect(page).toHaveURL(/#\/tasks\/task-tabs\?tab=conversation$/);
  await expect(main.getByText("Nächster Schritt")).toBeVisible();
  await expect(main.getByText("Kommentare & Entscheidungen")).toBeVisible();

  const tabsAreBeforeTitle = await main.evaluate((element) => {
    const tab = element.querySelector<HTMLElement>('[role="tab"]');
    const title = element.querySelector("h2");
    return Boolean(tab && title && (tab.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING));
  });
  expect(tabsAreBeforeTitle).toBe(true);
});

test("starts a delivery run once and navigates to the created run", async ({ page }) => {
  let requests = 0;
  await page.route("**/tasks/task-tabs/runs", async (route) => {
    requests += 1;
    await new Promise((resolve) => setTimeout(resolve, 100));
    await route.fulfill({ status: 303, headers: { location: "/runs/run-new" } });
  });
  await page.goto("/app/#/tasks/task-tabs");
  const button = page.getByRole("button", { name: "Delivery Agent" });
  await button.click();
  await expect(button).toBeDisabled();
  await expect(page).toHaveURL(/#\/runs\/run-new$/);
  expect(requests).toBe(1);
});

test("shows an actionable delivery-start error", async ({ page }) => {
  await page.route("**/tasks/task-tabs/runs", (route) =>
    route.fulfill({ status: 409, contentType: "text/plain", body: "Repository-Ziel fehlt" }),
  );
  await page.goto("/app/#/tasks/task-tabs");
  await page.getByRole("button", { name: "Delivery Agent" }).click();
  await expect(page.getByText(/Repository-Ziel fehlt/)).toBeVisible();
});
