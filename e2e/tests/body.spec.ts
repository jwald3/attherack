import { test, expect, type Page } from "@playwright/test";
import { acceptDialogs, pngBuffer, randomYear, uid } from "./helpers";

// Bodyweight and the Progress tab (measurements + photos). Rows here are keyed
// by date, so each test works inside its own random past year.

test.describe("bodyweight", () => {
  async function logWeight(page: Page, weight: string, date: string) {
    const form = page.locator("form.bw-form");
    await form.locator('[name="weight"]').fill(weight);
    await form.locator('[name="date"]').fill(date);
    await form.getByRole("button", { name: "Log" }).click();
  }
  const row = (page: Page, date: string) => page.locator(".bw-row", { has: page.locator(".bw-row-date", { hasText: date }) });

  test("logs weigh-ins, overwrites a date, charts them, and deletes one", async ({ page }) => {
    const y = randomYear();
    await page.goto("/bodyweight");
    await logWeight(page, "200.4", `${y}-03-01`);
    await logWeight(page, "198", `${y}-03-08`);
    await expect(row(page, `${y}-03-01`).locator(".bw-row-wt")).toHaveText("200.4");
    await expect(row(page, `${y}-03-08`).locator(".bw-row-wt")).toHaveText("198");

    // A second weigh-in on the same date replaces the first.
    await logWeight(page, "197.5", `${y}-03-08`);
    await expect(row(page, `${y}-03-08`)).toHaveCount(1);
    await expect(row(page, `${y}-03-08`).locator(".bw-row-wt")).toHaveText("197.5");

    await expect(page.locator("#bw-content svg.bw-svg")).toBeVisible();
    const entries = page.locator("#bw-content .stat", { hasText: "Entries" }).locator(".stat-val");
    expect(Number(await entries.textContent())).toBeGreaterThanOrEqual(2);

    await row(page, `${y}-03-01`).getByRole("button", { name: "Delete" }).click();
    await expect(row(page, `${y}-03-01`)).toHaveCount(0);
    await page.reload();
    await expect(row(page, `${y}-03-01`)).toHaveCount(0);
    await expect(row(page, `${y}-03-08`)).toHaveCount(1);
  });

  test("ignores a zero weight", async ({ page }) => {
    const y = randomYear();
    await page.goto("/bodyweight");
    await logWeight(page, "0", `${y}-06-01`);
    await expect(page.locator("#bw-content")).toBeVisible();
    await expect(row(page, `${y}-06-01`)).toHaveCount(0);
  });
});

test.describe("progress", () => {
  test("saves measurements, charts a site over time, and deletes one", async ({ page }) => {
    const y = randomYear();
    await page.goto("/progress");
    const form = page.locator("form.measure-form");
    const save = async (date: string, values: Record<string, string>) => {
      for (const [site, v] of Object.entries(values)) await form.locator(`[name="${site}"]`).fill(v);
      await form.locator('[name="date"]').fill(date);
      await form.getByRole("button", { name: "Save measurements" }).click();
    };

    // Blank fields are skipped; only the sites filled in are logged.
    await save(`${y}-01-10`, { waist: "34.5", neck: "16" });
    const waist = page.locator(".measure-card", { has: page.locator(".measure-card-label", { hasText: /^Waist$/ }) });
    await expect(waist).toBeVisible();
    await expect(page.locator(".measure-card", { hasText: "Neck" })).toBeVisible();

    await page.reload();
    await save(`${y}-02-10`, { waist: "33.75" });
    await expect(waist.locator(".measure-chart svg")).toBeVisible();

    const history = waist.locator(".measure-history");
    await history.locator("summary").click();
    const rowOn = (date: string) => history.locator(".bw-row", { has: page.locator(".bw-row-date", { hasText: date }) });
    await expect(rowOn(`${y}-01-10`).locator(".bw-row-wt")).toHaveText("34.5");
    await expect(rowOn(`${y}-02-10`).locator(".bw-row-wt")).toHaveText("33.75");

    await rowOn(`${y}-01-10`).getByRole("button", { name: "Delete" }).click();
    await waist.locator(".measure-history summary").click();
    await expect(rowOn(`${y}-01-10`)).toHaveCount(0);
    await expect(rowOn(`${y}-02-10`)).toHaveCount(1);
  });

  test("uploads a progress photo to the gallery and deletes it", async ({ page }) => {
    acceptDialogs(page);
    const pose = `front ${uid()}`;
    await page.goto("/progress");
    const form = page.locator("#photo-form");
    await form.locator('input[type="file"]').setInputFiles({ name: "me.png", mimeType: "image/png", buffer: await pngBuffer(page, 60, 90) });
    await expect(form.locator(".filepick-name")).toHaveText("me.png");
    await form.locator('[name="pose"]').fill(pose);
    await form.locator('[name="date"]').fill("2004-04-04");
    await form.getByRole("button", { name: "Upload" }).click();

    const photo = page.locator(".photo-item", { has: page.locator(".photo-pose", { hasText: pose }) });
    await expect(photo).toBeVisible();
    await expect(photo.locator(".photo-date")).toHaveText("2004-04-04");
    const size = await photo.locator("img").evaluate(async (el) => {
      const img = el as HTMLImageElement;
      if (!img.complete) await new Promise((r) => (img.onload = img.onerror = r));
      return [img.naturalWidth, img.naturalHeight];
    });
    expect(size).toEqual([60, 90]);
    // The form resets for the next upload.
    await expect(form.locator(".filepick-name")).toHaveText("");
    await expect(form.locator('[name="pose"]')).toHaveValue("");

    await photo.getByRole("button", { name: "Delete" }).click();
    await expect(photo).toHaveCount(0);
  });

  test("rejects a file that isn't an image", async ({ page }) => {
    const pose = `fake ${uid()}`;
    await page.goto("/progress");
    const form = page.locator("#photo-form");
    const upload = page.waitForResponse((r) => r.url().endsWith("/progress/photos"));
    await form.locator('input[type="file"]').setInputFiles({ name: "notes.png", mimeType: "image/png", buffer: Buffer.from("not a png") });
    await form.locator('[name="pose"]').fill(pose);
    await form.getByRole("button", { name: "Upload" }).click();
    const res = await upload;
    expect(res.status()).toBe(400);
    expect(await res.text()).toContain("isn't a supported image");
    await page.reload();
    await expect(page.locator(".photo-pose", { hasText: pose })).toHaveCount(0);
  });
});
