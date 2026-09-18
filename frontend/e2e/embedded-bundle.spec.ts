import { expect, test } from "@playwright/test";

test("production app serves matching build metadata and every fingerprinted asset", async ({ page, request }) => {
  const indexResponse = await request.get("/app/");
  expect(indexResponse.ok()).toBeTruthy();
  const index = await indexResponse.text();
  expect(index).toContain('<div id="root">');

  const references = [...index.matchAll(/(?:src|href)="([^"]+)"/g)].map((match) => match[1]);
  expect(references.length).toBeGreaterThan(0);
  for (const reference of references) {
    expect(reference).toMatch(/-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$/);
    const assetResponse = await request.get(new URL(reference, "http://taskboard.test/app/").pathname);
    expect(assetResponse.ok(), reference).toBeTruthy();
  }

  const buildInfo = await (await request.get("/app/build-info.json")).json();
  expect(buildInfo.version).toBeTruthy();
  expect(buildInfo.commit).toBeTruthy();
  await page.goto("/app/");
  await expect(page.locator("#root")).toBeVisible();
});
