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

test("disables the clicked delivery agent until the start request completes", async ({ page }) => {
  let releaseStartRequest!: () => void;
  const startRequestReleased = new Promise<void>((resolve) => { releaseStartRequest = resolve; });
  let startRequests = 0;
  await page.route("**/tasks/task-tabs/runs", async (route) => {
    startRequests += 1;
    await startRequestReleased;
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });

  await page.goto("/app/#/tasks/task-tabs?tab=conversation");
  const startButton = page.getByRole("button", { name: "Delivery Agent" });

  await startButton.click();
  await expect(startButton).toBeDisabled();
  await startButton.click({ force: true });
  expect(startRequests).toBe(1);

  releaseStartRequest();
  await expect(startButton).toBeEnabled();
});
