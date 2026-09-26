import { test, expect } from "@playwright/test";
import { attach, coachRequests, imageSize, pngBuffer, reply, resetFake, send, userBubble } from "./helpers";

test.beforeEach(async ({ page, request }) => {
  await resetFake(request);
  await page.goto("/");
  await expect(page.locator("#chat-attach")).toBeEnabled();
});

test("attaches a photo with a caption and the coach receives it", async ({ page, request }) => {
  await attach(page, [{ name: "machine.png", mimeType: "image/png", buffer: await pngBuffer(page, 64, 48) }]);
  await send(page, "what machine is this?");

  // The thread now has a URL, the user bubble shows the stored thumbnail,
  // and the composer's preview strip is cleared for the next message.
  await expect(page).toHaveURL(/\/c\/\d+$/);
  const thumb = userBubble(page).locator(".bubble-images img");
  await expect(thumb).toHaveAttribute("src", /^\/chat\/img\/\d+$/);
  expect((await imageSize(page, ".bubble.user .bubble-images img")).width).toBe(64);
  await expect(userBubble(page)).toContainText("what machine is this?");
  await expect(page.locator(".chat-preview")).toHaveCount(0);

  await expect(reply(page)).toContainText("pec deck");

  // The API request carried the image block first, then the caption.
  const [req] = await coachRequests(request);
  const turn = req.messages[req.messages.length - 1];
  expect(turn.role).toBe("user");
  expect(turn.content.map((p) => p.type)).toEqual(["image", "text"]);
  expect(turn.content[0].source).toMatchObject({ type: "base64", media_type: "image/png" });
  expect(turn.content[0].source.data.length).toBeGreaterThan(50);
  expect(turn.content[1].text).toBe("what machine is this?");
});

test("downscales large photos in the browser before upload", async ({ page, request }) => {
  await attach(page, [{ name: "huge.png", mimeType: "image/png", buffer: await pngBuffer(page, 3000, 2000) }]);
  await send(page, "physique check");
  await expect(reply(page)).toContainText("pec deck");

  // Longest edge capped at 1568px, re-encoded as JPEG.
  const size = await imageSize(page, ".bubble.user .bubble-images img");
  expect(size.width).toBe(1568);
  expect(size.height).toBe(Math.round(2000 * (1568 / 3000)));
  const [req] = await coachRequests(request);
  const img = req.messages.at(-1)!.content[0];
  expect(img.source.media_type).toBe("image/jpeg");
});

test("sends a photo with no caption and titles the thread", async ({ page, request }) => {
  await attach(page, [{ name: "gym.png", mimeType: "image/png", buffer: await pngBuffer(page, 40, 40) }]);
  await page.locator("#chat-input").press("Enter");

  await expect(userBubble(page).locator(".bubble-images img")).toBeVisible();
  await expect(reply(page)).toContainText("pec deck");
  const [req] = await coachRequests(request);
  const turn = req.messages.at(-1)!;
  expect(turn.content[0].type).toBe("image");
  expect(turn.content[1].text).toBe("(photo attached, no caption)");

  // The fake title model names the thread once the reply lands.
  await expect(page.locator(".thread.active .thread-link")).toHaveText("Pec deck question");
  await expect(page.locator(".coach-title")).toHaveText("Pec deck question");
});

test("earlier photos stay in context for follow-up questions", async ({ page, request }) => {
  await attach(page, [{ name: "gym.png", mimeType: "image/png", buffer: await pngBuffer(page, 40, 40) }]);
  await send(page, "what is this?");
  await expect(reply(page)).toContainText("pec deck");

  await send(page, "how do I set it up?");
  await expect(reply(page)).toContainText("No photo attached. You said: how do I set it up?");

  const reqs = await coachRequests(request);
  expect(reqs).toHaveLength(2);
  const history = reqs[1].messages;
  expect(history.map((m) => m.role)).toEqual(["user", "assistant", "user"]);
  expect(history[0].content[0].type).toBe("image"); // reloaded from the database
  expect(history[2].content.map((p) => p.type)).toEqual(["text"]);
});

test("photos survive a reload of the thread", async ({ page }) => {
  await attach(page, [{ name: "gym.png", mimeType: "image/png", buffer: await pngBuffer(page, 40, 40) }]);
  await send(page, "keep this");
  await expect(reply(page)).toContainText("pec deck");

  await page.reload();
  const thumb = page.locator(".bubble.user .bubble-images img");
  await expect(thumb).toHaveCount(1);
  expect((await imageSize(page, ".bubble.user .bubble-images img")).width).toBe(40);
  await expect(page.locator(".bubble.assistant")).toContainText("pec deck");
});

test("pasting an image into the composer attaches it", async ({ page, request }) => {
  await page.locator("#chat-input").evaluate((el) => {
    const c = document.createElement("canvas");
    c.width = 30;
    c.height = 20;
    c.getContext("2d")!.fillRect(0, 0, 30, 20);
    return new Promise<void>((resolve) =>
      c.toBlob((blob) => {
        const dt = new DataTransfer();
        dt.items.add(new File([blob!], "shot.png", { type: "image/png" }));
        el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
        resolve();
      }, "image/png"),
    );
  });
  await expect(page.locator(".chat-preview")).toHaveCount(1);
  await expect(page.locator(".chat-preview.busy")).toHaveCount(0);

  await send(page, "pasted");
  await expect(reply(page)).toContainText("pec deck");
  const [req] = await coachRequests(request);
  expect(req.messages.at(-1)!.content[0].type).toBe("image");
});

test("removing a preview sends the message without it", async ({ page, request }) => {
  await attach(page, [{ name: "gym.png", mimeType: "image/png", buffer: await pngBuffer(page, 40, 40) }]);
  await page.locator(".chat-preview .remove").click();
  await expect(page.locator(".chat-preview")).toHaveCount(0);

  await send(page, "just words");
  await expect(reply(page)).toContainText("No photo attached. You said: just words");
  await expect(userBubble(page).locator(".bubble-images")).toHaveCount(0);
  const [req] = await coachRequests(request);
  expect(req.messages.at(-1)!.content.map((p) => p.type)).toEqual(["text"]);
});

test("rejects a file that only claims to be an image", async ({ page, request }) => {
  // The browser can't decode it, so the original bytes go up and the server's
  // content sniffing turns it away without creating a message.
  await attach(page, [{ name: "fake.jpg", mimeType: "image/jpeg", buffer: Buffer.from("definitely not a jpeg") }]);
  await send(page, "is this real?");

  const hint = page.locator(".composer-hint");
  await expect(hint).toHaveClass(/chat-err/);
  await expect(hint).toContainText("isn't a supported image");
  await expect(page.locator("#chat-input")).toHaveValue("is this real?");
  await expect(page.locator("#chat-col .bubble")).toHaveCount(0);
  await expect(page).toHaveURL(/\/$/);
  expect(await coachRequests(request)).toHaveLength(0);
});

test("caps attachments at four per message", async ({ page }) => {
  const buf = await pngBuffer(page, 10, 10);
  const files = [1, 2, 3, 4, 5].map((i) => ({ name: `p${i}.png`, mimeType: "image/png", buffer: buf }));
  await page.locator("#chat-files").setInputFiles(files);
  await expect(page.locator(".chat-preview")).toHaveCount(4);
  await expect(page.locator(".composer-hint")).toContainText("up to 4 photos");
});
