import { expect, test, type Page } from "@playwright/test";

import {
  DECISION_ID,
  explain,
  MEMORY_ID,
  memoryPage,
  overview,
  VENDOR_URL,
} from "../src/test/fixtures";

/** Serves the recorded run, so the journey is tested without a database. */
async function mockApi(page: Page) {
  await page.route("**/api/v1/overview", (route) => route.fulfill({ json: overview }));
  await page.route(`**/api/v1/decisions/${DECISION_ID}/explain`, (route) =>
    route.fulfill({ json: explain }),
  );
  await page.route(`**/api/v1/memories/${MEMORY_ID}`, (route) =>
    route.fulfill({ json: memoryPage }),
  );
}

test("a visitor reaches the proof in three clicks", async ({ page }) => {
  await mockApi(page);
  await page.goto("/");

  // The demo says what the visitor is looking at.
  await expect(page.getByText("This is a recorded run.")).toBeVisible();

  // Overview: the critical alert is the thing to look at, said in words a
  // visitor can read without knowing the vocabulary.
  await expect(
    page.getByRole("heading", { name: /Something the assistant read chose what send_email did/ }),
  ).toBeVisible();
  await page.getByRole("link", { name: /See how this happened/ }).click();

  // Decision: what the agent did, and the four levels of evidence.
  await expect(page.getByRole("heading", { name: "send_email()" })).toBeVisible();
  await expect(page.getByText("Retrieved")).toBeVisible();
  await expect(page.getByText("Supplied a value to `to`")).toBeVisible();
  await expect(page.getByText("Removing it changed the decision in 8 of 8 samples")).toBeVisible();

  // The path back to the vendor page that caused all of it.
  const path = page.getByLabel("Where this value came from");
  await expect(path).toContainText(VENDOR_URL);
  await expect(path).toContainText("From outside");

  // The replay card: both arms, the interval, the verdict.
  const replay = page.getByTestId("replay-card");
  await expect(replay).toContainText("With the memory");
  await expect(replay).toContainText("Without the memory");
  await expect(replay.getByText("strong", { exact: true })).toBeVisible();
  await expect(page.getByTestId("replay-sentence")).toContainText("8 of 8 samples");
  await expect(replay.getByRole("img", { name: /Effect 100 percent/ })).toBeVisible();

  // From there to the memory itself and its lineage.
  await page.getByRole("link", { name: "acme.billing_address", exact: true }).first().click();
  await expect(page.getByRole("heading", { name: "acme.billing_address" })).toBeVisible();
  await expect(page.getByText("crm/memories/acme.billing_address")).toBeVisible();
});

test("the demo cannot be made to spend money", async ({ page }) => {
  await mockApi(page);
  await page.goto(`/decisions/${DECISION_ID}`);
  await expect(page.getByRole("heading", { name: "send_email()" })).toBeVisible();
  // The recorded replay is shown, but there is no way to start another one.
  await expect(page.getByRole("button", { name: /Replay without this memory/ })).toHaveCount(0);
  await expect(page.getByTestId("replay-card")).toContainText("never runs the assistant's tools");
});

test("the page is readable on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockApi(page);
  await page.goto(`/decisions/${DECISION_ID}`);
  await expect(page.getByRole("heading", { name: "send_email()" })).toBeVisible();
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
});
