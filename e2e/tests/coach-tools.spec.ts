import { test, expect, type Page } from "@playwright/test";
import { appToday, coachRequests, randomYear, reply, resetFake, send, uid } from "./helpers";

// Every coach tool, driven through the real tool-use loop via the fake API's
// <<tool:NAME {json}>> directive, then checked in the UI where it shows up.

test.beforeEach(async ({ page, request }) => {
  await resetFake(request);
  await page.goto("/");
  await expect(page.locator("#chat-input")).toBeEnabled();
});

/** Sends one tool call and returns the coach's reply text. */
async function callTool(page: Page, name: string, input: object) {
  const before = await page.locator(".bubble.assistant:not(.pending)").count();
  await send(page, `<<tool:${name} ${JSON.stringify(input)}>>`);
  await expect(page.locator(".bubble.assistant:not(.pending)")).toHaveCount(before + 1);
  return (await reply(page).textContent())!;
}

test("sets: log, read back, correct, and delete", async ({ page }) => {
  const ex = `Tool Bench ${uid()}`;
  const logged = await callTool(page, "log_set", { exercise: ex, weight: 185, reps: 5, date: "2006-06-06" });
  const id = Number(logged.match(/Logged set #(\d+)/)![1]);

  let out = await callTool(page, "get_exercise_history", { exercise: ex.toLowerCase() });
  expect(out).toContain(`set #${id} — 2006-06-06: ${ex} 185x5`);

  // Omitted fields keep their values: only reps changes.
  out = await callTool(page, "update_set", { id, reps: 6 });
  expect(out).toContain(`Updated set #${id}: ${ex} 185x6 on 2006-06-06.`);
  await expect(reply(page)).toHaveAttribute("data-refresh-log", "1");

  out = await callTool(page, "set_workout_notes", { notes: `felt strong ${ex}`, date: "2006-06-06" });
  expect(out).toContain("Saved notes for 2006-06-06.");

  out = await callTool(page, "list_workouts", { limit: 100 });
  expect(out).toContain(`"exercise":"${ex}"`);

  await page.goto("/training");
  const workout = page.locator(".workout", { has: page.locator(`[data-exercise-name="${ex}"]`) });
  await expect(workout.locator(".workout-notes")).toHaveText(`felt strong ${ex}`);
  await expect(workout.locator(".s-load")).toHaveText("185 × 6");

  await page.goto("/");
  out = await callTool(page, "delete_set", { id });
  expect(out).toContain(`Deleted set #${id}: ${ex} 185x6 on 2006-06-06.`);
  out = await callTool(page, "delete_set", { id });
  expect(out).toContain(`No set with id #${id}.`);
  out = await callTool(page, "get_exercise_history", { exercise: ex });
  expect(out).toContain(`No logged sets for "${ex}" yet.`);
});

test("exercises: search the library and add a custom one", async ({ page }) => {
  let out = await callTool(page, "search_exercises", { query: "deadlift", equipment: "barbell", limit: 50 });
  expect(out).toContain('"name":"Barbell Deadlift"');
  expect(out).toContain('"equipment":"barbell"');

  const name = `Tool Pulldown ${uid()}`;
  out = await callTool(page, "create_exercise", { name, primary_muscles: ["lats"], equipment: "cable", category: "strength" });
  expect(out).toContain(`Added custom exercise "${name}".`);
  out = await callTool(page, "create_exercise", { name });
  expect(out).toContain("already exists in the library");
  await expect(reply(page)).not.toHaveAttribute("data-refresh-log", "1");

  await page.goto("/training");
  await page.locator("#ex-q").fill(name);
  await expect(page.locator(`.ex-card.custom[data-name="${name}"] .chip.equip`)).toHaveText("cable");
});

test("cardio: log a session and read the history", async ({ page }) => {
  const type = `Tool Cycling ${uid()}`;
  let out = await callTool(page, "log_cardio", { type, duration_minutes: 45, distance_miles: 12.5 });
  expect(out).toContain(`Logged cardio: ${type} on ${await appToday(page)} (45 min, 12.50 mi).`);
  out = await callTool(page, "get_cardio_history", {});
  expect(out).toContain(`${type} 12.50mi 45min`);

  await page.goto("/cardio");
  const row = page.locator(".cardio-row", { has: page.locator(`[data-cardio-type="${type}"]`) });
  await expect(row.locator(".cardio-dur")).toHaveText("45m");
});

test("bodyweight: log a weigh-in and read the history", async ({ page }) => {
  const date = `${randomYear()}-07-04`;
  let out = await callTool(page, "log_bodyweight", { weight: 181.2, date });
  expect(out).toContain(`Recorded bodyweight 181.2 on ${date}.`);
  out = await callTool(page, "get_bodyweight_history", { limit: 1000 });
  expect(out).toContain(`${date}: 181.2`);

  await page.goto("/bodyweight");
  await expect(page.locator(".bw-row", { hasText: date }).locator(".bw-row-wt")).toHaveText("181.2");
});

test("measurements: log valid and invalid sites, read one site and all", async ({ page }) => {
  const y = randomYear();
  let out = await callTool(page, "log_measurement", { site: "arm_l", value: 15.5, date: `${y}-01-01` });
  expect(out).toContain(`Logged left arm 15.5 on ${y}-01-01.`);
  await callTool(page, "log_measurement", { site: "arm_l", value: 15.75, date: `${y}-02-01` });

  // An unknown site is explained to the model, not logged.
  out = await callTool(page, "log_measurement", { site: "forehead", value: 9 });
  expect(out).toContain('Unknown measurement site "forehead". Valid sites: waist, chest');
  await expect(reply(page)).not.toHaveAttribute("data-refresh-log", "1");

  out = await callTool(page, "get_measurement_history", { site: "arm_l" });
  expect(out).toContain("Left arm history (oldest first):");
  expect(out.indexOf(`${y}-01-01: 15.5`)).toBeLessThan(out.indexOf(`${y}-02-01: 15.75`));
  out = await callTool(page, "get_measurement_history", {});
  expect(out).toContain("Latest measurement per site:");
  expect(out).toContain("Left arm:");

  await page.goto("/progress");
  const card = page.locator(".measure-card", { has: page.locator(".measure-card-label", { hasText: /^Left arm$/ }) });
  await card.locator(".measure-history summary").click();
  await expect(card.locator(".bw-row", { hasText: `${y}-02-01` }).locator(".bw-row-wt")).toHaveText("15.75");
});

test("supplements and food: history tools, and today's snapshot in the prompt", async ({ page, request }) => {
  const supp = `Zinc ${uid()}`;
  const food = `Tool Omelette ${uid()}`;
  await callTool(page, "log_supplement", { name: supp, amount: 15, unit: "mg" });
  await callTool(page, "log_food", { name: food, meal: "breakfast", notes: "3 eggs", calories: 300 });

  let out = await callTool(page, "get_supplement_history", {});
  expect(out).toContain(`${supp} 15mg`);
  out = await callTool(page, "get_food_history", { days: 1 });
  expect(out).toContain(`[breakfast] ${food} (3 eggs) — 300 kcal`);

  // Every request carries a fresh snapshot of today's data in the system prompt.
  const last = (await coachRequests(request)).at(-1)!;
  expect(last.system).toContain(`Supplements taken today:`);
  expect(last.system).toContain(`${supp} 15mg`);
  expect(last.system).toMatch(new RegExp(`Food logged today: .*${food}`));
});

test("a name is required where the tools say so", async ({ page }) => {
  let out = await callTool(page, "log_food", { name: "   " });
  expect(out).toBe("Tool results: ERROR error: name is required");
  out = await callTool(page, "start_program", {});
  expect(out).toBe("Tool results: ERROR error: provide a program id or name");
  out = await callTool(page, "start_program", { name: `Nope ${uid()}` });
  expect(out).toContain("Use list_programs to see what exists.");
  out = await callTool(page, "create_program", { name: "Empty", exercises: [{ exercise: " " }] });
  expect(out).toBe("Tool results: ERROR error: a program needs at least one exercise");
});
