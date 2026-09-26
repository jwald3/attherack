import { test, expect, type Page } from "@playwright/test";
import { uid } from "./helpers";

async function logCardio(page: Page, type: string, minutes: string, miles: string, date?: string) {
  const form = page.locator("form.cardio-form");
  await form.locator('[name="type"]').fill(type);
  await form.locator('[name="minutes"]').fill(minutes);
  await form.locator('[name="miles"]').fill(miles);
  if (date) await form.locator('[name="date"]').fill(date);
  await form.getByRole("button", { name: "Log" }).click();
}

const stat = (page: Page, label: string) => page.locator("#cardio-content .stat", { hasText: label }).locator(".stat-val");

test("logs a session today, updates the weekly totals, and deletes it", async ({ page }) => {
  await page.goto("/cardio");
  const type = `Rowing ${uid()}`;
  const before = parseFloat((await stat(page, "Sessions/wk").textContent())!);

  await logCardio(page, type, "30", "3");
  const row = page.locator(".cardio-row", { has: page.locator(`[data-cardio-type="${type}"]`) });
  await expect(row).toHaveCount(1);
  await expect(row.locator(".cardio-detail")).toContainText("3.00 mi");
  await expect(row.locator(".cardio-dur")).toHaveText("30m");
  await expect(stat(page, "Sessions/wk")).toHaveText(String(before + 1));

  await row.getByRole("button", { name: "Delete" }).click();
  await expect(row).toHaveCount(0);
  await expect(stat(page, "Sessions/wk")).toHaveText(String(before));
});

test("the type drawer shows totals, pace and a pace chart", async ({ page }) => {
  await page.goto("/cardio");
  const type = `Trail Run ${uid()}`;
  await logCardio(page, type, "30", "3", "2003-05-01"); // 10:00/mi
  await logCardio(page, type, "27", "3", "2003-05-08"); // 9:00/mi

  await page.locator(`.cardio-link[data-cardio-type="${type}"]`).first().click();
  const drawer = page.locator("#drawer");
  await expect(drawer.locator(".drawer-title")).toHaveText(type);
  await expect(drawer.locator(".drawer-meta-note")).toHaveText("2003-05-01 → 2003-05-08");
  const s = (label: string) => drawer.locator(".stat", { has: page.locator(".stat-label", { hasText: new RegExp(`^${label}$`) }) }).locator(".stat-val");
  await expect(s("Sessions")).toHaveText("2");
  await expect(s("Total mi")).toHaveText("6.0");
  await expect(s("Total time")).toHaveText("57m");
  await expect(s("Avg pace")).toHaveText("9:30/mi");
  await expect(s("Best pace")).toHaveText("9:00/mi");

  // Lower pace is better, so 10:00 → 9:00 is styled as an improvement.
  const label = drawer.locator(".drawer-chart-label");
  await expect(label).toContainText("Pace per session (/mi) · 10:00 →");
  await expect(label.locator(".up-good")).toHaveText("9:00");
  await expect(drawer.locator(".drawer-chart svg")).toBeVisible();

  const rows = drawer.locator(".ch-row:not(.ch-head)");
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText("2003-05-08");
  await expect(rows.first()).toContainText("9:00/mi");
  await expect(rows.first()).toContainText("6.7 mph");
});
