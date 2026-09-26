import { test, expect } from "@playwright/test";
import { DEMO_URL } from "../playwright.config";

// Basic accessibility checks on the populated demo instance: every control
// has an accessible name (icon-only buttons rely on aria-label), every form
// field is identifiable, and images have alt text.
test.use({ baseURL: DEMO_URL });

const PAGES = ["/", "/c/1", "/training", "/programs", "/cardio", "/bodyweight", "/progress", "/diet", "/supplements"];

for (const path of PAGES) {
  test(`${path}: controls and images are labelled`, async ({ page }) => {
    await page.goto(path);
    const problems = await page.evaluate(() => {
      const out: string[] = [];
      const describe = (el: Element) => el.outerHTML.slice(0, 120);
      const name = (el: Element) =>
        (el.getAttribute("aria-label") || el.getAttribute("title") || el.textContent || "").trim();

      for (const el of document.querySelectorAll("button, a[href]")) {
        if (!name(el)) out.push(`no accessible name: ${describe(el)}`);
      }
      for (const el of document.querySelectorAll("input:not([type=hidden]), select, textarea")) {
        const labelled =
          el.getAttribute("aria-label") || el.getAttribute("title") || el.getAttribute("placeholder") || el.closest("label");
        if (!labelled && !(el as HTMLElement).hidden) out.push(`unlabelled field: ${describe(el)}`);
      }
      for (const img of document.querySelectorAll("img")) {
        if (!img.hasAttribute("alt")) out.push(`img without alt: ${describe(img)}`);
      }
      // Decorative icons are hidden from screen readers.
      for (const svg of document.querySelectorAll("svg.icon")) {
        if (svg.getAttribute("aria-hidden") !== "true") out.push(`icon not aria-hidden: ${describe(svg)}`);
      }
      return out;
    });
    expect(problems).toEqual([]);
  });
}

test("the page language and titles are set", async ({ page }) => {
  await page.goto("/training");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page).toHaveTitle("At The Rack — Training");
  // Charts carry a text alternative.
  await page.goto("/bodyweight");
  await expect(page.locator("svg.bw-svg")).toHaveAttribute("role", "img");
  await expect(page.locator("svg.bw-svg")).toHaveAttribute("aria-label", /.+/);
});
