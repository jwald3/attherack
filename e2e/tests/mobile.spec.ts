import { test, expect, type Page } from "@playwright/test";
import { reply, resetFake, send, uid } from "./helpers";

// Runs in the "mobile" project (a Pixel 7 viewport with touch): the layout
// must fit the screen and stay usable without hover or zooming.

const PAGES = ["/", "/training", "/programs", "/cardio", "/bodyweight", "/progress", "/diet", "/supplements"];

async function horizontalOverflow(page: Page) {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
}

for (const path of PAGES) {
  test(`${path} fits the screen and its inputs won't trigger zoom`, async ({ page }) => {
    await page.goto(path);
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);

    const problems = await page.evaluate(() => {
      const out: string[] = [];
      const vw = document.documentElement.clientWidth;
      const visible = (el: Element) => (el as HTMLElement).offsetParent !== null && getComputedStyle(el).visibility !== "hidden";
      const label = (el: Element) => `${el.tagName.toLowerCase()}[${el.getAttribute("name") || el.textContent!.trim().slice(0, 20)}]`;
      for (const el of document.querySelectorAll("main input, main select, main textarea, main button")) {
        if (!visible(el) || (el as HTMLInputElement).type === "hidden" || (el as HTMLInputElement).type === "file") continue;
        // iOS zooms into inputs under 16px.
        if (!(el instanceof HTMLButtonElement) && parseFloat(getComputedStyle(el).fontSize) < 16) out.push(`under 16px: ${label(el)}`);
        // Nothing may be pushed past the right edge (clipped, not scrollable).
        if (el.closest(".ch-table")) continue; // the cardio mini-table scrolls sideways by design
        if (el.closest(".coach-side:not(.open)")) continue; // the closed drawer is off-screen by design
        const r = el.getBoundingClientRect();
        if (r.right > vw + 1 || r.left < -1) out.push(`off-screen: ${label(el)} (${Math.round(r.left)}–${Math.round(r.right)} of ${vw})`);
      }
      return out;
    });
    expect(problems).toEqual([]);

    // The current tab is scrolled into view in the sideways tab strip.
    await expect(page.locator(".tab.active")).toBeInViewport({ ratio: 0.9 });
  });
}

for (const path of ["/", "/cardio", "/bodyweight", "/progress", "/diet", "/supplements"]) {
  test(`${path} doesn't scroll the page itself (only its content area)`, async ({ page }) => {
    await page.goto(path);
    // Full-height pages subtract the top bar; a wrong height makes the whole
    // page scroll a little and pushes the bottom (e.g. the chat composer) out.
    expect(await page.evaluate(() => document.documentElement.scrollHeight - window.innerHeight)).toBeLessThanOrEqual(1);
  });
}

test("the coach composer sits fully on screen", async ({ page }) => {
  await page.goto("/");
  const box = (await page.locator("#chat-form").boundingBox())!;
  expect(box.y + box.height).toBeLessThanOrEqual(page.viewportSize()!.height);
  // The desktop keyboard hint is hidden on phones.
  await expect(page.locator(".composer-hint")).toBeHidden();
});

test("Training keeps the set form on the first screen and the library close by", async ({ page }) => {
  await page.goto("/training");
  await expect(page.getByRole("button", { name: "Add set" })).toBeInViewport();
  // History scrolls inside its panel instead of growing with every workout.
  const history = page.locator(".col-log > .panel.grow");
  const vh = page.viewportSize()!.height;
  expect((await history.boundingBox())!.height).toBeLessThanOrEqual(vh * 0.7 + 1);
  // The filter chips start collapsed so the library isn't a screen of chips.
  await expect(page.locator("details.facets")).not.toHaveAttribute("open");
});

test("the coach's conversation list is a slide-out panel", async ({ page, request }) => {
  await resetFake(request);
  await page.goto("/");
  const side = page.locator("#coach-side");
  const inView = () => side.evaluate((el) => el.getBoundingClientRect().right > 0);

  expect(await inView()).toBe(false);
  await page.locator(".side-toggle").tap();
  await expect.poll(inView).toBe(true);
  // It's a full-height drawer, not tucked under a header of a guessed height.
  expect((await side.boundingBox())!.y).toBe(0);
  // The open panel covers the toggle, so a tap beside it closes it.
  const vw = page.viewportSize()!;
  await page.touchscreen.tap(vw.width - 20, vw.height / 2);
  await expect.poll(inView).toBe(false);
  // Taps inside the panel (e.g. opening the key settings) leave it open.
  await page.locator(".side-toggle").tap();
  await expect.poll(inView).toBe(true);
  await page.locator(".side-key").tap();
  await expect(page.locator("#settings-wrap")).toHaveClass(/open/);
  expect(await inView()).toBe(true);
  await page.touchscreen.tap(vw.width - 20, vw.height / 2);
  await expect.poll(inView).toBe(false);

  // Chatting works with the on-screen send button.
  await page.locator("#chat-input").fill("from my phone");
  await page.locator(".composer .send").tap();
  await expect(reply(page)).toContainText("You said: from my phone");
});

test("row delete buttons are visible without hover and easy to tap", async ({ page }) => {
  const ex = `Phone Curl ${uid()}`;
  await page.goto("/training");
  const form = page.locator("form.addset");
  await form.locator('[name="exercise"]').fill(ex);
  await form.locator('[name="weight"]').fill("30");
  await form.locator('[name="reps"]').fill("12");
  await form.getByRole("button", { name: "Add set" }).tap();

  const del = page.locator("#log tr", { has: page.locator(`[data-exercise-name="${ex}"]`) }).getByRole("button", { name: "Delete set" });
  await expect(del).toBeVisible();
  expect(await del.evaluate((el) => getComputedStyle(el).opacity)).toBe("1");
  const box = (await del.boundingBox())!;
  expect(Math.min(box.width, box.height)).toBeGreaterThanOrEqual(24);

  await del.tap();
  await expect(page.locator(`#log [data-exercise-name="${ex}"]`)).toHaveCount(0);
});

test("the tab bar scrolls sideways instead of widening the page", async ({ page }) => {
  await page.goto("/supplements");
  // The last tab is reachable (the bar scrolls) and the page itself doesn't.
  const last = page.locator("nav.tabs .tab", { hasText: "Supplements" });
  await last.scrollIntoViewIfNeeded();
  await expect(last).toBeInViewport();
  expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
  await last.tap();
  await expect(page).toHaveURL(/\/supplements$/);
});

test("the Training drawer fits a phone", async ({ page }) => {
  await page.goto("/training");
  await page.locator("#ex-q").fill("Barbell Deadlift");
  await page.locator('.ex-card[data-name="Barbell Deadlift"] .ex-link').tap();
  const drawer = page.locator("#drawer");
  await expect(drawer.locator(".drawer-title")).toHaveText("Barbell Deadlift");
  // Once it has slid in, it sits fully on screen.
  const vw = page.viewportSize()!.width;
  await expect
    .poll(async () => {
      const box = (await drawer.boundingBox())!;
      return box.x >= 0 && box.x + box.width <= vw + 1;
    })
    .toBe(true);
  await drawer.getByRole("button", { name: "Close" }).tap();
  await expect(drawer).toBeHidden();
});
