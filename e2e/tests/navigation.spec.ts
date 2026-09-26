import { test, expect } from "@playwright/test";

const TABS = [
  { name: "Coach", path: "/", heading: ".coach-title" },
  { name: "Training", path: "/training", heading: "text=Log a set" },
  { name: "Programs", path: "/programs", heading: "text=Your programs" },
  { name: "Cardio", path: "/cardio", heading: ".panel-title >> text=Cardio" },
  { name: "Bodyweight", path: "/bodyweight", heading: "text=Bodyweight trend" },
  { name: "Progress", path: "/progress", heading: "text=Body measurements" },
  { name: "Diet", path: "/diet", heading: ".panel-title >> text=Diet" },
  { name: "Supplements", path: "/supplements", heading: ".panel-title >> text=Supplements" },
];

test("every tab loads cleanly and is reachable from the nav", async ({ page }) => {
  // Any script error or failed request on any page fails the test.
  const problems: string[] = [];
  page.on("pageerror", (e) => problems.push(`page error: ${e.message}`));
  // (The app has no favicon; the browser's automatic request for one 404s.)
  const favicon = (s: string) => s.includes("favicon.ico");
  page.on("console", (m) => m.type() === "error" && !favicon(m.location().url) && problems.push(`console: ${m.text()}`));
  page.on("response", (r) => r.status() >= 400 && !favicon(r.url()) && problems.push(`${r.status()} ${r.url()}`));

  await page.goto("/");
  for (const tab of TABS) {
    await page.locator("nav.tabs").getByRole("link", { name: tab.name, exact: true }).click();
    await expect(page).toHaveURL(tab.path === "/" ? /\/$/ : new RegExp(tab.path + "$"));
    await expect(page.locator(".tab.active")).toHaveText(tab.name);
    await expect(page.locator(tab.heading).first()).toBeVisible();
    await expect(page).toHaveTitle(tab.name === "Coach" ? "At The Rack — Coach" : `At The Rack — ${tab.name}`);
  }
  // Styles and scripts are served from the embedded assets.
  expect(await page.evaluate(() => typeof (window as any).htmx)).toBe("object");
  expect(await page.locator("body").evaluate((b) => getComputedStyle(b).backgroundColor)).not.toBe("rgba(0, 0, 0, 0)");
  expect(problems).toEqual([]);
});

test("unknown pages and threads are handled", async ({ page }) => {
  expect((await page.goto("/no-such-page"))!.status()).toBe(404);

  // A thread that doesn't exist sends you back to a new chat.
  await page.goto("/c/999999");
  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator(".coach-title")).toHaveText("New chat");
});
