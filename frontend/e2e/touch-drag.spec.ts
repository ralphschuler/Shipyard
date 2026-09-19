import { expect, test, type Page } from "@playwright/test";

const board = {
  Board: { ID: "board-touch", Name: "Touch board" },
  Columns: [
    { ID: "todo", Name: "Todo" },
    { ID: "doing", Name: "Doing" },
  ],
  Tasks: [
    { ID: "task-a", BoardID: "board-touch", ColumnID: "todo", Title: "Touch task", Description: "", Priority: "normal", Labels: [] },
  ],
  Labels: [],
  Projects: [],
  Groups: [],
};

async function mockBoard(page: Page) {
  const moves: string[] = [];
  await page.route("**/api/v1/boards/board-touch", (route) => route.fulfill({ json: board }));
  await page.route("**/tasks/task-a/move", async (route) => {
    moves.push(String(route.request().postData() || ""));
    await route.fulfill({ status: 204, body: "" });
  });
  return moves;
}

async function touchCard(page: Page) {
  return page.getByText("Touch task", { exact: true }).locator("..");
}

test("swiping a task card does not reorder it and keeps native touch scrolling", async ({ page }) => {
  const moves = await mockBoard(page);
  await page.goto("/app/#/boards/board-touch");
  const card = await touchCard(page);
  await expect(card).toHaveCSS("touch-action", "auto");
  await card.dispatchEvent("pointerdown", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: 100, clientY: 100 });
  await card.dispatchEvent("pointermove", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: 140, clientY: 104 });
  await card.dispatchEvent("pointerup", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: 140, clientY: 104 });
  await page.waitForTimeout(500);
  expect(moves).toHaveLength(0);
  await expect(card).not.toHaveAttribute("data-touch-dragging", "true");
});

test("moving beyond the tolerance before the delay cancels the pending drag", async ({ page }) => {
  const moves = await mockBoard(page);
  await page.goto("/app/#/boards/board-touch");
  const card = await touchCard(page);
  await card.dispatchEvent("pointerdown", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: 100, clientY: 100 });
  await page.waitForTimeout(100);
  await card.dispatchEvent("pointermove", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: 112, clientY: 100 });
  await page.waitForTimeout(450);
  expect(moves).toHaveLength(0);
  await expect(card).not.toHaveAttribute("data-touch-dragging", "true");
});

test("holding a task card activates touch dragging after the delay", async ({ page }) => {
  await mockBoard(page);
  await page.goto("/app/#/boards/board-touch");
  const card = await touchCard(page);
  await card.dispatchEvent("pointerdown", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: 100, clientY: 100 });
  await page.waitForTimeout(250);
  await expect(card).not.toHaveAttribute("data-touch-dragging", "true");
  await page.waitForTimeout(75);
  await expect(card).toHaveAttribute("data-touch-dragging", "true");
  await card.dispatchEvent("pointercancel", { pointerId: 1, pointerType: "touch", isPrimary: true });
});

test("a long press followed by a drop moves the task to the target column", async ({ page }) => {
  const moves = await mockBoard(page);
  await page.goto("/app/#/boards/board-touch");
  const card = await touchCard(page);
  const target = page.locator('[data-board-column="doing"]');
  await card.dispatchEvent("pointerdown", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: 100, clientY: 100 });
  await page.waitForTimeout(500);
  await expect(card).toHaveAttribute("data-touch-dragging", "true");
  const box = await target.boundingBox();
  if (!box) throw new Error("Target column is not visible");
  await card.dispatchEvent("pointerup", { pointerId: 1, pointerType: "touch", isPrimary: true, clientX: box.x + 40, clientY: box.y + 80 });
  await expect.poll(() => moves).toHaveLength(1);
  expect(moves[0]).toContain("target_column_id=doing");
});
