import { test, expect, type Page } from "@playwright/test";
import { acceptDialogs, reply, resetFake, send, uid } from "./helpers";

// User-entered text is echoed back in many places (bubbles, titles, lists,
// data attributes, drawers). Markup in it must stay inert text everywhere, and
// names with quotes or ampersands must still work as links and lookups.

// If any payload ever executes, it sets this flag.
const payload = (tag: string) => `<img src=x onerror="window.__xss='${tag}'">`;

async function assertNoXSS(page: Page) {
  // Give any broken <img> a moment to fire onerror.
  await page.waitForTimeout(200);
  expect(await page.evaluate(() => (window as any).__xss)).toBeUndefined();
}

test.beforeEach(async ({ request }) => {
  await resetFake(request);
});

test("chat messages, replies and thread titles stay text", async ({ page }) => {
  acceptDialogs(page, payload("rename"));
  await page.goto("/");
  const msg = payload("chat");
  await send(page, msg);
  await expect(reply(page)).toContainText(`You said: ${msg}`);
  await expect(page.locator("#chat-col .bubble.user").last()).toHaveText(msg);
  await assertNoXSS(page);

  // Rename the thread to markup too, then reload the stored conversation.
  await page.locator(".thread.active .thread-rename").click();
  await expect(page.locator(".thread.active .thread-link")).toHaveText(payload("rename"));
  await page.reload();
  await expect(page.locator(".coach-title")).toHaveText(payload("rename"));
  await expect(page.locator("#chat-col .bubble.user")).toHaveText(msg);
  await assertNoXSS(page);
});

test("the coach's Markdown renders, including tables, but raw HTML doesn't", async ({ page }) => {
  await page.goto("/");
  await send(page, "**bold** and `code` and *soft*\n\n| Lift | Top |\n|---|---:|\n| Squat | 225 |\n\n- one\n- <b>two</b>");
  const r = reply(page);
  await expect(r.locator("strong")).toHaveText("bold");
  await expect(r.locator("code")).toHaveText("code");
  await expect(r.locator("em")).toHaveText("soft");
  await expect(r.locator("table th")).toHaveText(["Lift", "Top"]);
  await expect(r.locator("table td").nth(1)).toHaveText("225");
  await expect(r.locator("table td").nth(1)).toHaveAttribute("style", "text-align:right");
  await expect(r.locator("li")).toHaveText(["one", "<b>two</b>"]);
  await expect(r.locator("li b")).toHaveCount(0);
});

test("names on the tracking tabs stay text", async ({ page }) => {
  acceptDialogs(page);
  const p = payload("tabs");

  await page.goto("/training");
  const set = page.locator("form.addset");
  await set.locator('[name="exercise"]').fill(p);
  await set.locator('[name="reps"]').fill("1");
  await set.getByRole("button", { name: "Add set" }).click();
  await expect(page.locator(".ex-link", { hasText: p }).first()).toBeVisible();
  await page.locator(".ex-link", { hasText: p }).first().click();
  await expect(page.locator("#drawer .drawer-title")).toHaveText(p);
  await page.keyboard.press("Escape");
  await page.locator(".ex-add-box summary").click();
  await page.locator("#add-name").fill(`${p} ${uid()}`);
  await page.locator(".ex-add-form").getByRole("button", { name: "Add exercise" }).click();
  await expect(page.locator("#ex-add-result .ex-add-msg.ok")).toContainText(p);
  await assertNoXSS(page);

  await page.goto("/cardio");
  await page.locator('form.cardio-form [name="type"]').fill(p);
  await page.locator("form.cardio-form").getByRole("button", { name: "Log" }).click();
  await page.locator(".cardio-link", { hasText: p }).first().click();
  await expect(page.locator("#drawer .drawer-title")).toHaveText(p);
  await assertNoXSS(page);

  await page.goto("/diet");
  await page.locator('form.diet-form [name="name"]').fill(p);
  await page.locator('form.diet-form [name="notes"]').fill(p);
  await page.locator("form.diet-form").getByRole("button", { name: "Log" }).click();
  await expect(page.locator(".food-name", { hasText: p }).first()).toBeVisible();
  await assertNoXSS(page);

  await page.goto("/supplements");
  await page.locator('form.supp-form [name="name"]').fill(p);
  await page.locator("form.supp-form").getByRole("button", { name: "Log" }).click();
  // The quick-log chip re-posts the name from a hidden input: it must round-trip exactly.
  const chip = page.locator(".supp-chip", { has: page.locator(".supp-chip-name", { hasText: p }) });
  await chip.click();
  await expect(page.locator(".drawer-history .dh-day").first().locator(".supp-log", { hasText: p })).toHaveCount(2);
  await assertNoXSS(page);

  await page.goto("/programs");
  await page.locator('#prog-form [name="name"]').fill(p);
  await page.locator('#prog-form .prog-row [name="exercise"]').fill(p);
  await page.locator("#prog-form").getByRole("button", { name: "Save program" }).click();
  await expect(page.locator(".program-name", { hasText: p }).first()).toBeVisible();
  await assertNoXSS(page);
});

test("names with quotes and ampersands work as links and lookups", async ({ page }) => {
  const ex = `Farmer's "Heavy" Walk & Carry ${uid()}`;
  await page.goto("/training");
  const form = page.locator("form.addset");
  await form.locator('[name="exercise"]').fill(ex);
  await form.locator('[name="weight"]').fill("100");
  await form.locator('[name="reps"]').fill("1");
  await form.getByRole("button", { name: "Add set" }).click();

  await page.locator(".ex-link", { hasText: ex }).first().click();
  const drawer = page.locator("#drawer");
  await expect(drawer.locator(".drawer-title")).toHaveText(ex);
  await expect(drawer.locator(".stat", { hasText: "Total sets" }).locator(".stat-val")).toHaveText("1");

  const type = `Stairs & "Steps" ${uid()}`;
  await page.goto("/cardio");
  await page.locator('form.cardio-form [name="type"]').fill(type);
  await page.locator('form.cardio-form [name="minutes"]').fill("10");
  await page.locator("form.cardio-form").getByRole("button", { name: "Log" }).click();
  await page.locator(".cardio-link", { hasText: type }).click();
  await expect(drawer.locator(".drawer-title")).toHaveText(type);
  await expect(drawer.locator(".stat", { hasText: "Sessions" }).locator(".stat-val")).toHaveText("1");
});
