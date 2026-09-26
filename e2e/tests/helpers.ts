import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { FAKE_URL } from "../playwright.config";

export type FakeRequest = {
  at: number;
  model: string;
  system?: string;
  messages: { role: string; content: any[] }[];
};

export async function resetFake(request: APIRequestContext) {
  await request.post(`${FAKE_URL}/_reset`);
}

/** Requests the coach model (not the title model) has received. */
export async function coachRequests(request: APIRequestContext): Promise<FakeRequest[]> {
  const res = await request.get(`${FAKE_URL}/_requests`);
  const all = (await res.json()) as FakeRequest[];
  return all.filter((r) => !r.model.includes("haiku"));
}

/** Builds a PNG of the given size in the browser and returns it as a buffer. */
export async function pngBuffer(page: Page, width: number, height: number): Promise<Buffer> {
  const dataURL = await page.evaluate(
    ([w, h]) => {
      const c = document.createElement("canvas");
      c.width = w;
      c.height = h;
      const ctx = c.getContext("2d")!;
      ctx.fillStyle = "#d94f2a";
      ctx.fillRect(0, 0, w, h);
      ctx.fillStyle = "#ffffff";
      ctx.fillRect(w / 4, h / 4, w / 2, h / 2);
      return c.toDataURL("image/png");
    },
    [width, height],
  );
  return Buffer.from(dataURL.split(",")[1], "base64");
}

/** Attaches files through the hidden file input and waits for downscaling. */
export async function attach(page: Page, files: { name: string; mimeType: string; buffer: Buffer }[]) {
  await page.locator("#chat-files").setInputFiles(files);
  await expect(page.locator(".chat-preview")).toHaveCount(Math.min(files.length, 4));
  await expect(page.locator(".chat-preview.busy")).toHaveCount(0);
}

export async function send(page: Page, text: string) {
  const input = page.locator("#chat-input");
  await input.fill(text);
  await input.press("Enter");
}

/** The coach's finished reply (the pending placeholder polls until it lands). */
export function reply(page: Page) {
  return page.locator(".bubble.assistant:not(.pending)").last();
}

export function userBubble(page: Page) {
  return page.locator("#chat-col .bubble.user:not(.optimistic)").last();
}

/** Natural size of an <img> once it has loaded. */
export async function imageSize(page: Page, selector: string) {
  return page.locator(selector).last().evaluate(async (el) => {
    const img = el as HTMLImageElement;
    if (!img.complete) await new Promise((r) => (img.onload = img.onerror = r));
    return { width: img.naturalWidth, height: img.naturalHeight };
  });
}
