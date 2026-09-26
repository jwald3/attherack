import { test, expect } from "@playwright/test";
import { NOKEY_URL } from "../playwright.config";
import { reply, send } from "./helpers";

// These run against the second app instance, which starts without an API key.
// They share its state, so they run in order.
test.describe.configure({ mode: "serial" });
test.use({ baseURL: NOKEY_URL });

test("without a key the coach is disabled and says how to enable it", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator(".coach-head .tag.off")).toHaveText("key needed");
  await expect(page.locator(".chat-empty-note")).toContainText("Add your Anthropic API key");
  await expect(page.locator("#chat-input")).toBeDisabled();
  await expect(page.locator("#chat-attach")).toBeDisabled();
  // The key panel starts open.
  await expect(page.locator("#settings-panel")).toBeVisible();
  await expect(page.locator("#settings-panel")).toContainText("Coach disabled");

  // The AI fill on the Training tab explains what's missing.
  await page.goto("/training");
  await page.locator(".ex-add-box summary").click();
  await page.locator("#add-name").fill("Cable Row");
  await page.locator("#ai-fill").click();
  await expect(page.locator("#ex-add-result .ex-add-msg.err")).toContainText("Add your API key");
});

test("saving a key enables the coach; removing it disables it again", async ({ page }) => {
  await page.goto("/");
  const panel = page.locator("#settings-panel");
  const keyInput = panel.locator('[name="api_key"]');

  await panel.getByRole("button", { name: "Save key" }).click();
  await expect(panel.locator(".settings-msg.err")).toHaveText("Paste a key first.");

  await keyInput.fill("not-a-key");
  await panel.getByRole("button", { name: "Save key" }).click();
  await expect(panel.locator(".settings-msg.err")).toContainText("doesn't look like an Anthropic key");

  // The fake API accepts exactly this key.
  await page.locator('#settings-panel [name="api_key"]').fill("sk-ant-e2e");
  await page.locator("#settings-panel").getByRole("button", { name: "Save key" }).click();
  await expect(page.locator("#settings-panel .settings-msg.ok")).toContainText("Key saved");
  // The page reloads itself with the coach switched on.
  await expect(page.locator("#chat-input")).toBeEnabled();
  await expect(page.locator(".coach-head .tag.off")).toHaveCount(0);

  await send(page, "hello from a saved key");
  await expect(reply(page)).toContainText("You said: hello from a saved key");

  await page.locator(".side-key").click();
  await page.locator("#settings-panel").getByRole("button", { name: "Remove" }).click();
  await expect(page.locator("#settings-panel .settings-msg.ok")).toContainText("Key removed");
  await expect(page.locator("#chat-input")).toBeDisabled();
  await expect(page.locator(".coach-head .tag.off")).toHaveText("key needed");
});
