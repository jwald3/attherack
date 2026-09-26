import { test, expect } from "@playwright/test";
import { acceptDialogs, appToday, coachRequests, reply, resetFake, send, uid, userBubble } from "./helpers";

// Conversations with the coach. The fake API follows <<...>> directives in the
// message (see fake-claude.mjs) so these tests can script tool calls, errors
// and slow replies.

test.beforeEach(async ({ page, request }) => {
  await resetFake(request);
  await page.goto("/");
  await expect(page.locator("#chat-input")).toBeEnabled();
});

test("a new chat gets a URL, a sidebar entry and an AI title", async ({ page, request }) => {
  await send(page, "how's my bench trending?");
  await expect(page).toHaveURL(/\/c\/\d+$/);
  await expect(userBubble(page)).toHaveText("how's my bench trending?");
  await expect(reply(page)).toHaveText("No photo attached. You said: how's my bench trending?");
  await expect(page.locator(".thread.active .thread-link")).toHaveText("Pec deck question");
  await expect(page.locator(".coach-title")).toHaveText("Pec deck question");

  // The coach got today's date, a data snapshot and the full tool list.
  const [req] = await coachRequests(request);
  expect(req.system).toContain(`Today's date: ${await appToday(page)}`);
  expect(req.system).toContain("Recent training snapshot:");
  expect(req.tools).toEqual(expect.arrayContaining(["log_set", "get_bodyweight_history", "start_program", "log_food"]));

  // Reloading the thread shows the stored conversation.
  await page.reload();
  await expect(page.locator(".bubble.user")).toHaveText("how's my bench trending?");
  await expect(page.locator(".bubble.assistant")).toContainText("You said: how's my bench trending?");
});

test("follow-ups stay in the same thread and carry the history", async ({ page, request }) => {
  await send(page, "first");
  await expect(reply(page)).toContainText("You said: first");
  const url = page.url();
  await send(page, "second");
  await expect(reply(page)).toContainText("You said: second");
  expect(page.url()).toBe(url);
  await expect(page.locator("#chat-col .bubble")).toHaveCount(4);

  const reqs = await coachRequests(request);
  expect(reqs[1].messages.map((m) => m.role)).toEqual(["user", "assistant", "user"]);
});

test("Shift+Enter adds a line; Enter sends", async ({ page }) => {
  const input = page.locator("#chat-input");
  await input.fill("line one");
  await input.press("Shift+Enter");
  await input.pressSequentially("line two");
  await expect(page.locator("#chat-col .bubble")).toHaveCount(0);
  await input.press("Enter");
  await expect(userBubble(page).locator("br")).toHaveCount(1);
  await expect(userBubble(page)).toContainText("line one");
  await expect(userBubble(page)).toContainText("line two");
  await expect(input).toHaveValue("");
});

test("a starter prompt sends itself", async ({ page }) => {
  await page.locator(".starter", { hasText: "Plan a pull day for me" }).click();
  await expect(reply(page)).toContainText("You said: Plan a pull day for me");
});

test("the coach logs a set through a tool, and it shows up in Training", async ({ page, request }) => {
  const ex = `Coach Squat ${uid()}`;
  await send(page, `did 5 at 225 <<tool:log_set {"exercise":"${ex}","weight":225,"reps":5,"rpe":8}>>`);

  const today = await appToday(page);
  await expect(reply(page)).toContainText(`Tool results: Logged set #`);
  await expect(reply(page)).toContainText(`${ex} 225x5 on ${today}`);
  // A reply that changed data carries the marker that refreshes the log.
  await expect(reply(page)).toHaveAttribute("data-refresh-log", "1");

  // Two API calls: the tool_use turn, then the turn carrying its result.
  const reqs = await coachRequests(request);
  expect(reqs).toHaveLength(2);
  const [asked, result] = reqs[1].messages.slice(-2);
  expect(asked.role).toBe("assistant");
  expect(asked.content.find((p) => p.type === "tool_use")).toMatchObject({ name: "log_set" });
  expect(result.content[0]).toMatchObject({ type: "tool_result" });
  expect(result.content[0].tool_use_id).toBe(asked.content.find((p) => p.type === "tool_use").id);

  await page.goto("/training");
  const row = page.locator(".workout").first().locator("tr", { has: page.locator(`[data-exercise-name="${ex}"]`) });
  await expect(row.locator(".s-load")).toHaveText("225 × 5 @8");
});

test("several tool calls in one turn all run", async ({ page }) => {
  const supp = `Magnesium ${uid()}`;
  const food = `Oats ${uid()}`;
  await send(
    page,
    `took ${supp} and ate ${food}` +
      ` <<tool:log_supplement {"name":"${supp}","amount":400,"unit":"mg"}>>` +
      ` <<tool:log_food {"name":"${food}","meal":"breakfast","protein":10}>>`,
  );
  await expect(reply(page)).toContainText(`Logged supplement: ${supp} 400mg`);
  await expect(reply(page)).toContainText(`Logged food on`);
  await expect(reply(page)).toContainText(`[breakfast] ${food} — 10g protein`);

  await page.goto("/supplements");
  await expect(page.locator(".supp-chip", { hasText: supp })).toBeVisible();
  await page.goto("/diet");
  await expect(page.locator(".food-name", { hasText: food })).toBeVisible();
});

test("the coach reads data back through tools", async ({ page }) => {
  const name = `Legs ${uid()}`;
  await send(page, `<<tool:create_program {"name":"${name}","exercises":[{"exercise":"Squat","sets":3,"reps":5}]}>>`);
  await expect(reply(page)).toContainText(`Created program "${name}" with 1 exercises`);

  await send(page, `<<tool:start_program {"name":"${name.toLowerCase()}","date":"2005-05-05"}>>`);
  await expect(reply(page)).toContainText(`Started "${name}": logged 3 sets to 2005-05-05.`);

  await send(page, `<<tool:list_programs {}>>`);
  await expect(reply(page)).toContainText(`"name":"${name}"`);
  await expect(reply(page)).not.toHaveAttribute("data-refresh-log", "1"); // read-only
});

test("a failing tool is reported back to the model, not to the user as a crash", async ({ page, request }) => {
  await send(page, `fix it <<tool:update_set {"id":0}>>`);
  await expect(reply(page)).toHaveText("Tool results: ERROR error: id is required");
  await expect(reply(page)).not.toHaveAttribute("data-refresh-log", "1");
  const reqs = await coachRequests(request);
  expect(reqs[1].messages.at(-1)!.content[0]).toMatchObject({ type: "tool_result", is_error: true });
});

test("an API error shows in the reply bubble and the chat keeps working", async ({ page }) => {
  await send(page, "<<error 529 Overloaded>>");
  await expect(reply(page)).toHaveText("Something went wrong talking to Claude: claude api (529): Overloaded");
  // No AI title for a failed first exchange; the thread keeps its opening words.
  await expect(page.locator(".thread.active .thread-link")).toHaveText("<<error 529 Overloaded>>");

  await send(page, "try again");
  await expect(reply(page)).toContainText("You said: try again");
});

test("a reply still arrives after reloading mid-generation", async ({ page }) => {
  await send(page, "take your time <<slow 2500>>");
  await expect(page).toHaveURL(/\/c\/\d+$/);
  await expect(page.locator(".bubble.assistant.pending")).toBeVisible();

  // The reply is generated server-side; the reloaded page resumes polling.
  await page.reload();
  await expect(page.locator(".bubble.assistant.pending")).toBeVisible();
  await expect(reply(page)).toContainText("You said: take your time", { timeout: 15_000 });
  await expect(page.locator(".bubble.assistant.pending")).toHaveCount(0);
});

test("renames and deletes conversations from the sidebar", async ({ page }) => {
  const title = `Renamed ${uid()}`;
  acceptDialogs(page, title);

  await send(page, "keep me");
  await expect(reply(page)).toBeVisible();
  const keepURL = page.url();
  await page.locator(".thread.active .thread-rename").click();
  await expect(page.locator(".thread.active .thread-link")).toHaveText(title);
  await expect(page.locator(".coach-title")).toHaveText(title);
  await page.reload();
  await expect(page.locator(".thread.active .thread-link")).toHaveText(title);

  // A second conversation, via "New chat".
  await page.locator(".new-chat").click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator(".coach-title")).toHaveText("New chat");
  await send(page, "delete me");
  await expect(reply(page)).toBeVisible();
  const deleteURL = page.url();

  // Deleting another thread only refreshes the sidebar…
  const kept = page.locator(".thread", { has: page.locator(`a[href="${new URL(keepURL).pathname}"]`) });
  await expect(kept).toHaveCount(1);
  // …and deleting the open one sends you to a new chat.
  await page.locator(".thread.active").getByRole("button", { name: "Delete" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator(`a[href="${new URL(deleteURL).pathname}"]`)).toHaveCount(0);
  await expect(kept).toHaveCount(1);

  await page.goto(keepURL);
  await page.locator(".thread.active").getByRole("button", { name: "Delete" }).click();
  await expect(page).toHaveURL(/\/$/);
  await page.goto(keepURL);
  await expect(page).toHaveURL(/\/$/); // gone
});

test("the key panel is read-only when the key comes from the environment", async ({ page }) => {
  await page.locator(".side-key").click();
  const panel = page.locator("#settings-panel");
  await expect(panel).toContainText("Coach enabled");
  await expect(panel.locator(".key-src")).toHaveText("via env");
  await expect(panel.locator(".key-preview")).toHaveText("••••"); // never the key itself
  await expect(panel.locator("form.key-form")).toHaveCount(0);
});
