import { test, expect, type Page } from "@playwright/test";
import { uid } from "./helpers";

// The Diet and Supplements tabs.

test.describe("diet", () => {
  const food = (page: Page, name: string) => page.locator(".food-row", { has: page.locator(".food-name", { hasText: name }) });

  test("logs food with and without macros under today, then deletes it", async ({ page }) => {
    const id = uid();
    await page.goto("/diet");
    const form = page.locator("form.diet-form");

    await form.locator('[name="name"]').fill(`Burrito bowl ${id}`);
    await form.locator('[name="meal"]').selectOption("lunch");
    await form.locator('[name="notes"]').fill("double chicken");
    await form.locator(".diet-macros summary").click();
    await form.locator('[name="calories"]').fill("750");
    await form.locator('[name="protein"]').fill("62");
    await form.getByRole("button", { name: "Log" }).click();

    const bowl = food(page, `Burrito bowl ${id}`);
    await expect(bowl).toBeVisible();
    await expect(bowl.locator(".food-meal")).toHaveText("lunch");
    await expect(bowl.locator(".food-notes")).toHaveText("double chicken");
    await expect(bowl.locator(".food-macros")).toHaveText("750 kcal · 62p");

    // The form clears for the next entry (the meal stays).
    await expect(form.locator('[name="name"]')).toHaveValue("");
    await expect(form.locator('[name="calories"]')).toHaveValue("");
    await expect(form.locator('[name="meal"]')).toHaveValue("lunch");

    // A name alone is enough.
    await form.locator('[name="name"]').fill(`Apple ${id}`);
    await form.getByRole("button", { name: "Log" }).click();
    const apple = food(page, `Apple ${id}`);
    await expect(apple).toBeVisible();
    await expect(apple.locator(".food-macros")).toHaveCount(0);

    // Both are under "Today", whose totals include the bowl's macros.
    const today = page.locator(".diet-day", { has: page.locator(".dh-date", { hasText: /^Today$/ }) });
    await expect(today.locator(".food-name", { hasText: id })).toHaveCount(2);
    await expect(today.locator(".diet-totals")).toContainText("kcal");

    // Logged names feed the autocomplete.
    await expect(page.locator(`#food-names option[value="Apple ${id}"]`)).toHaveCount(1);

    await apple.getByRole("button", { name: "Delete" }).click();
    await expect(apple).toHaveCount(0);
    await expect(bowl).toBeVisible();
  });

  test("files food logged for a past date under that date", async ({ page }) => {
    const name = `Leftovers ${uid()}`;
    await page.goto("/diet");
    const form = page.locator("form.diet-form");
    // Inside the page's 30-day window, but not today.
    const date = await page.evaluate(() => {
      const d = new Date();
      d.setDate(d.getDate() - 3);
      return d.toLocaleDateString("sv"); // YYYY-MM-DD in local time
    });
    await form.locator('[name="name"]').fill(name);
    await form.locator('[name="date"]').fill(date);
    await form.getByRole("button", { name: "Log" }).click();
    const day = page.locator(".diet-day", { has: page.locator(".food-name", { hasText: name }) });
    await expect(day.locator(".dh-date")).toHaveText(date);
  });
});

test.describe("supplements", () => {
  const chip = (page: Page, name: string) => page.locator(".supp-chip", { has: page.locator(".supp-chip-name", { hasText: name }) });
  const todays = (page: Page, name: string) =>
    page.locator(".drawer-history .dh-day").first().locator(".supp-log", { hasText: name });

  test("logs a dose, re-logs it with one click, and deletes one", async ({ page }) => {
    const name = `Creatine ${uid()}`;
    await page.goto("/supplements");
    const form = page.locator("form.supp-form");
    await form.locator('[name="name"]').fill(name);
    await form.locator('[name="amount"]').fill("5");
    await form.locator("#supp-unit-select").selectOption("g");
    await form.getByRole("button", { name: "Log" }).click();

    // It becomes a one-click chip, already marked as taken today.
    const c = chip(page, name);
    await expect(c).toHaveClass(/done/);
    await expect(c.locator(".supp-chip-dose")).toHaveText("5 g");
    await expect(todays(page, name)).toHaveCount(1);
    await expect(page.locator(".supp-cons-row", { hasText: name }).locator(".supp-cons-n")).toHaveText("1/30 days");
    // The form resets.
    await expect(form.locator('[name="name"]')).toHaveValue("");
    await expect(form.locator("#supp-unit-select")).toHaveValue("");

    // The chip logs the same dose again (still one distinct day).
    await c.click();
    await expect(todays(page, name)).toHaveCount(2);
    await expect(page.locator(".supp-cons-row", { hasText: name }).locator(".supp-cons-n")).toHaveText("1/30 days");

    await todays(page, name).first().getByRole("button", { name: "Delete" }).click();
    await expect(todays(page, name)).toHaveCount(1);
  });

  test("'Other…' unit reveals a free-text unit", async ({ page }) => {
    const name = `Fish oil ${uid()}`;
    await page.goto("/supplements");
    const form = page.locator("form.supp-form");
    const custom = form.locator("#supp-unit-custom");
    await expect(custom).toBeHidden();
    await form.locator("#supp-unit-select").selectOption("__other");
    await expect(custom).toBeVisible();
    await expect(custom).toBeFocused();
    await custom.fill("softgels");
    await form.locator('[name="name"]').fill(name);
    await form.locator('[name="amount"]').fill("2");
    await form.getByRole("button", { name: "Log" }).click();

    await expect(chip(page, name).locator(".supp-chip-dose")).toHaveText("2 softgels");
    await expect(custom).toBeHidden();
  });
});
