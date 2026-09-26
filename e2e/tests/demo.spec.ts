import { test, expect } from "@playwright/test";
import { DEMO_URL } from "../playwright.config";
import { coachRequests, reply, resetFake, send } from "./helpers";

// Runs against the instance started with -seed-demo: about six weeks of
// training, cardio, bodyweight, supplements and food. Checks that every tab
// renders real-looking data (the empty states are covered elsewhere).
test.use({ baseURL: DEMO_URL });

test("every tab renders the seeded data without errors", async ({ page }) => {
  const problems: string[] = [];
  page.on("pageerror", (e) => problems.push(`page error: ${e.message}`));
  page.on("response", (r) => r.status() >= 400 && !r.url().includes("favicon.ico") && problems.push(`${r.status()} ${r.url()}`));

  // Coach: the welcome conversation, with its Markdown table rendered.
  await page.goto("/");
  await page.locator(".thread-link", { hasText: "Welcome to the demo" }).click();
  await expect(page.locator(".bubble.assistant table tr")).toHaveCount(6);
  await expect(page.locator(".bubble.assistant strong").first()).toHaveText("demo data");

  // Training: about 24 workouts on an upper/lower split.
  await page.goto("/training");
  expect(await page.locator(".workout").count()).toBeGreaterThanOrEqual(20);
  await expect(page.locator(".workout-notes").first()).toBeVisible();
  await expect(page.locator('#log [data-exercise-name="Barbell Squat"]').first()).toBeVisible();
  // Facet chips come from the ~870-exercise library.
  expect(await page.locator(".chip.pick.muscle").count()).toBeGreaterThan(10);

  await page.goto("/cardio");
  expect(await page.locator(".cardio-row").count()).toBeGreaterThan(20);
  const weekMiles = page.locator(".stat", { hasText: "This week" }).first().locator(".stat-val");
  expect(parseFloat((await weekMiles.textContent())!)).toBeGreaterThan(0);

  await page.goto("/bodyweight");
  await expect(page.locator(".bw-hero")).toBeVisible();
  await expect(page.locator("#bw-content svg.bw-svg path.line")).toBeVisible();
  // A slow cut: the total change is negative and styled as such.
  await expect(page.locator(".stat", { hasText: "Change" }).locator(".stat-val")).toHaveClass(/down/);
  expect(Number(await page.locator(".stat", { hasText: "Entries" }).locator(".stat-val").textContent())).toBeGreaterThan(20);

  await page.goto("/diet");
  const today = page.locator(".diet-day").first();
  await expect(today.locator(".dh-date")).toHaveText("Today");
  await expect(today.locator(".food-row")).toHaveCount(4);
  await expect(today.locator(".diet-totals")).toContainText("kcal");

  await page.goto("/supplements");
  expect((await page.locator(".supp-chip-name").allTextContents()).sort()).toEqual(["Creatine", "Vitamin D"]);
  const creatine = page.locator(".supp-cons-row", { hasText: "Creatine" }).locator(".supp-cons-n");
  expect(parseInt((await creatine.textContent())!)).toBeGreaterThan(20);

  // The demo has no programs or measurements: those tabs show their empty states.
  await page.goto("/programs");
  await expect(page.locator("#program-list .empty")).toBeVisible();
  await page.goto("/progress");
  await expect(page.locator("#measure-content .bw-empty-big")).toBeVisible();

  expect(problems).toEqual([]);
});

test("progression drawers show the demo's trends", async ({ page }) => {
  await page.goto("/training");
  // Deadlifts are trained once a week and go up 10 each week.
  await page.locator('#log [data-exercise-name="Barbell Deadlift"]').first().click();
  const drawer = page.locator("#drawer");
  await expect(drawer.locator(".drawer-title")).toHaveText("Barbell Deadlift");
  // Library metadata for a built-in exercise.
  await expect(drawer.locator(".drawer-meta .chip.muscle").first()).toBeVisible();
  expect(Number(await drawer.locator(".stat", { hasText: "Sessions" }).locator(".stat-val").textContent())).toBeGreaterThanOrEqual(5);
  await expect(drawer.locator(".drawer-chart-label .up-good")).toBeVisible();
  await page.keyboard.press("Escape");

  await page.goto("/cardio");
  await page.locator('.cardio-link[data-cardio-type="Running"]').first().click();
  await expect(drawer.locator(".drawer-chart-label")).toContainText("Pace per session (/mi)");
  // Pace improves week over week: lower is better, so it's marked as improved.
  await expect(drawer.locator(".drawer-chart-label .up-good")).toBeVisible();
  // Clicking the backdrop closes the drawer too.
  await page.locator("#drawer-backdrop").click({ position: { x: 5, y: 200 } });
  await expect(drawer).toBeHidden();
});

test("the coach's prompt snapshot reflects the demo data", async ({ page, request }) => {
  await resetFake(request);
  await page.goto("/");
  await send(page, "how am I doing?");
  await expect(reply(page)).toContainText("You said: how am I doing?");
  const [req] = await coachRequests(request);
  expect(req.system).toMatch(/Most recent weigh-in: [\d.]+ \(on \d{4}-\d{2}-\d{2}\)/);
  expect(req.system).toMatch(/Cardio last 7 days: \d+ sessions/);
  expect(req.system).toContain("Food logged today:");
  expect(req.system).toMatch(/- Barbell Squat: \d+x\d/);
});
