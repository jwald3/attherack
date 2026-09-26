import { test, expect, type Page } from "@playwright/test";
import { appToday, uid } from "./helpers";

test.beforeEach(async ({ page }) => {
  await page.goto("/training");
});

async function logSet(page: Page, exercise: string, weight: string, reps: string, rpe = "") {
  const form = page.locator("form.addset");
  await form.locator('[name="exercise"]').fill(exercise);
  await form.locator('[name="weight"]').fill(weight);
  await form.locator('[name="reps"]').fill(reps);
  await form.locator('[name="rpe"]').fill(rpe);
  await form.getByRole("button", { name: "Add set" }).click();
}

const setRow = (page: Page, exercise: string) => page.locator("#log tr", { has: page.locator(`[data-exercise-name="${exercise}"]`) });

test("logs sets into today's workout and deletes one", async ({ page }) => {
  const ex = `Test Squat ${uid()}`;
  await logSet(page, ex, "225", "5", "8");
  await logSet(page, ex, "235", "3");

  const rows = setRow(page, ex);
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0).locator(".s-load")).toHaveText("225 × 5 @8");
  await expect(rows.nth(1).locator(".s-load")).toHaveText("235 × 3");

  // Both sets land in the workout dated today (the newest one, listed first).
  const today = await appToday(page);
  await expect(page.locator(".workout").first().locator(".workout-date")).toHaveText(today);

  await rows.nth(0).getByRole("button", { name: "Delete set" }).click();
  await expect(rows).toHaveCount(1);
  await expect(rows.first().locator(".s-load")).toHaveText("235 × 3");

  // The log is persisted, not just swapped in.
  await page.reload();
  await expect(setRow(page, ex)).toHaveCount(1);
});

test("logs a set to a past date", async ({ page }) => {
  const ex = `Past Press ${uid()}`;
  await page.locator('form.addset [name="date"]').fill("2001-02-03");
  await logSet(page, ex, "95", "8");
  const workout = page.locator(".workout", { has: page.locator(`[data-exercise-name="${ex}"]`) });
  await expect(workout.locator(".workout-date")).toHaveText("2001-02-03");
});

test("searches the exercise library by name and by filter chips", async ({ page }) => {
  await expect(page.locator("#exercise-results")).toContainText("Search by name");

  await page.locator("#ex-q").fill("bench press");
  const cards = page.locator("#exercise-results .ex-card");
  await expect(cards.first()).toBeVisible();
  for (const name of await cards.locator(".ex-name").allTextContents()) {
    expect(name.toLowerCase()).toContain("bench press");
  }

  // A muscle chip narrows the results and can be cleared again.
  await page.locator("#ex-q").fill("");
  await page.locator('.chip.pick.muscle[data-val="lats"]').click();
  await expect(page.locator("#ex-muscle")).toHaveValue("lats");
  await expect(page.locator(".chip.pick.muscle.active")).toHaveCount(1);
  await expect(cards.first()).toBeVisible();
  await page.locator("#facet-clear").click();
  await expect(page.locator("#ex-muscle")).toHaveValue("");
  await expect(page.locator("#facet-clear")).toBeHidden();
});

test("'+ log' on a search result fills the set form", async ({ page }) => {
  await page.locator("#ex-q").fill("Barbell Deadlift");
  const card = page.locator('.ex-card[data-name="Barbell Deadlift"]');
  await card.getByRole("button", { name: "+ log" }).click();
  await expect(page.locator('form.addset [name="exercise"]')).toHaveValue("Barbell Deadlift");
  await expect(page.locator('form.addset [name="weight"]')).toBeFocused();
});

test("the history drawer shows bests, a chart and every set", async ({ page }) => {
  const ex = `Drawer Row ${uid()}`;
  const form = page.locator("form.addset");
  await form.locator('[name="date"]').fill("2002-01-01");
  await logSet(page, ex, "100", "10");
  await form.locator('[name="date"]').fill("2002-01-08");
  await logSet(page, ex, "110", "8");
  await logSet(page, ex, "110", "6");

  await page.locator(`#log .ex-link[data-exercise-name="${ex}"]`).first().click();
  const drawer = page.locator("#drawer");
  await expect(drawer).toBeVisible();
  await expect(drawer.locator(".drawer-title")).toHaveText(ex);
  const stat = (label: string) => drawer.locator(".stat", { hasText: label }).locator(".stat-val");
  await expect(stat("Best set")).toHaveText("110×8");
  await expect(stat("Est. 1RM")).toHaveText("139"); // Epley: 110 × (1 + 8/30)
  await expect(stat("Sessions")).toHaveText("2");
  await expect(stat("Total sets")).toHaveText("3");
  await expect(drawer.locator(".drawer-chart svg")).toBeVisible();
  await expect(drawer.locator(".drawer-chart-label")).toContainText("100 → 110");
  await expect(drawer.locator(".dh-day")).toHaveCount(2);

  // Escape closes it.
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
});

test("the drawer for an unlogged library exercise says so", async ({ page }) => {
  await page.locator("#ex-q").fill("Barbell Deadlift");
  await page.locator('.ex-card[data-name="Barbell Deadlift"] .ex-link').click();
  const drawer = page.locator("#drawer");
  await expect(drawer.locator(".drawer-meta .chip").first()).toBeVisible(); // library metadata
  await page.locator("#drawer .btn[aria-label=Close]").click();
  await expect(drawer).toBeHidden();
});

test("adds a custom exercise, which becomes searchable; duplicates are refused", async ({ page }) => {
  const name = `Zeta Pulldown ${uid()}`;
  await page.locator(".ex-add-box summary").click();
  const form = page.locator(".ex-add-form");
  await form.locator('[name="name"]').fill(name);
  // Mini-chips append muscles to the comma list.
  await form.locator('.mini-chip[data-target="add-primary"][data-val="lats"]').click();
  await form.locator('.mini-chip[data-target="add-primary"][data-val="biceps"]').click();
  await expect(form.locator('[name="primary_muscles"]')).toHaveValue("lats, biceps");
  await form.locator('[name="equipment"]').fill("cable");
  await form.getByRole("button", { name: "Add exercise" }).click();

  await expect(page.locator("#ex-add-result .ex-add-msg.ok")).toContainText(`Added “${name}.”`);
  await expect(form.locator('[name="name"]')).toHaveValue(""); // form resets

  await page.locator("#ex-q").fill(name);
  const card = page.locator(`.ex-card.custom[data-name="${name}"]`);
  await expect(card).toBeVisible();
  await expect(card.locator(".chip.custom-tag")).toHaveText("custom");
  await expect(card.locator(".chip.equip")).toHaveText("cable");

  await form.locator('[name="name"]').fill(name.toUpperCase());
  await form.getByRole("button", { name: "Add exercise" }).click();
  await expect(page.locator("#ex-add-result .ex-add-msg.err")).toHaveText("You already have an exercise with that name.");
});

test("the AI button fills in a custom exercise's fields", async ({ page }) => {
  await page.locator(".ex-add-box summary").click();
  const form = page.locator(".ex-add-form");

  await page.locator("#ai-fill").click();
  await expect(page.locator("#ex-add-result .ex-add-msg.err")).toHaveText("Enter a name first.");

  await form.locator('[name="name"]').fill("Parallel Grip Lat Pulldown");
  await page.locator("#ai-fill").click();
  await expect(page.locator("#ex-add-result .ex-add-msg.ok")).toContainText("Filled from AI");
  await expect(form.locator('[name="primary_muscles"]')).toHaveValue("lats");
  await expect(form.locator('[name="secondary_muscles"]')).toHaveValue("biceps");
  await expect(form.locator('[name="equipment"]')).toHaveValue("cable");
  await expect(form.locator('[name="category"]')).toHaveValue("strength");
  // The library search above has its own "equipment" field; it's left alone.
  await expect(page.locator("#ex-equipment")).toHaveValue("");
});
