import { test, expect } from "@playwright/test";
import { acceptDialogs, uid } from "./helpers";

test("builds a program, starts it into today's log, and deletes it", async ({ page }) => {
  acceptDialogs(page);
  const id = uid();
  const name = `Push Day ${id}`;
  const bench = `Prog Bench ${id}`;
  const dips = `Prog Dips ${id}`;

  await page.goto("/programs");
  const form = page.locator("#prog-form");
  await form.locator('[name="name"]').fill(name);
  await form.locator('[name="notes"]').fill("chest focus");

  // First row, then "+ Add exercise" clones a blank row (sets default 3).
  let row = form.locator(".prog-row").nth(0);
  await row.locator('[name="exercise"]').fill(bench);
  await row.locator('[name="sets"]').fill("2");
  await row.locator('[name="reps"]').fill("5");
  await row.locator('[name="weight"]').fill("185");
  await row.locator('[name="rpe"]').fill("8");
  await page.locator("#prog-add-row").click();
  await expect(form.locator(".prog-row")).toHaveCount(2);
  row = form.locator(".prog-row").nth(1);
  await expect(row.locator('[name="sets"]')).toHaveValue("3");
  await expect(row.locator('[name="exercise"]')).toHaveValue("");
  await row.locator('[name="exercise"]').fill(dips);
  await row.locator('[name="reps"]').fill("10");

  // A third, blank row is ignored on save.
  await page.locator("#prog-add-row").click();
  await form.getByRole("button", { name: "Save program" }).click();

  const card = page.locator(".program", { has: page.locator(".program-name", { hasText: name }) });
  await expect(card).toBeVisible();
  await expect(card.locator(".program-notes")).toHaveText("chest focus");
  await expect(card.locator("tr")).toHaveCount(2);
  await expect(card.locator("tr").nth(0).locator(".s-load")).toHaveText("2 × 5 @ 185 @8");
  await expect(card.locator("tr").nth(1).locator(".s-load")).toHaveText("3 × 10");

  // Saving resets the form to one blank row.
  await expect(form.locator(".prog-row")).toHaveCount(1);
  await expect(form.locator('[name="name"]')).toHaveValue("");

  // Start logs 2 + 3 sets to today.
  await card.getByRole("button", { name: "Start" }).click();
  await expect(card.locator(".prog-started")).toContainText("Logged 5 sets to today");
  await card.locator(".prog-started a").click();
  await expect(page).toHaveURL(/\/training$/);
  const today = page.locator(".workout").first();
  await expect(today.locator(`tr:has([data-exercise-name="${bench}"])`)).toHaveCount(2);
  await expect(today.locator(`tr:has([data-exercise-name="${dips}"])`)).toHaveCount(3);

  await page.goto("/programs");
  await card.getByRole("button", { name: "Delete program" }).click();
  await expect(card).toHaveCount(0);
});

test("removing a program row, and the last row just clears", async ({ page }) => {
  await page.goto("/programs");
  const form = page.locator("#prog-form");
  await form.locator('.prog-row [name="exercise"]').fill("Something");
  await page.locator("#prog-add-row").click();
  await form.locator(".prog-row").nth(1).getByRole("button", { name: "Remove exercise" }).click();
  await expect(form.locator(".prog-row")).toHaveCount(1);

  await form.locator(".prog-row").getByRole("button", { name: "Remove exercise" }).click();
  await expect(form.locator(".prog-row")).toHaveCount(1);
  await expect(form.locator('.prog-row [name="exercise"]')).toHaveValue("");
});
