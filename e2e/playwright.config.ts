import { defineConfig, devices } from "@playwright/test";

// The app under test is the real Go binary, built from the repo root and
// pointed at fake-claude.mjs via ANTHROPIC_BASE_URL. Two instances run: the
// main one has ANTHROPIC_API_KEY set (so the coach works and the key UI is
// locked), and a second one starts without a key, for the disabled-coach and
// save-a-key flows. A third is started with -seed-demo, to check every tab
// against realistic, populated data. Playwright starts every server and tears
// them down after the run; databases are recreated per run.
export const APP_URL = "http://127.0.0.1:18181";
export const NOKEY_URL = "http://127.0.0.1:18182";
export const DEMO_URL = "http://127.0.0.1:18183";
export const FAKE_URL = "http://127.0.0.1:19911";

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1, // one shared database and one request log on the fake API
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: APP_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] }, testIgnore: /mobile\.spec\.ts/ },
    // Phone-sized viewport with touch, for the mobile layout.
    { name: "mobile", use: { ...devices["Pixel 7"] }, testMatch: /mobile\.spec\.ts/ },
  ],
  webServer: [
    {
      command: "node fake-claude.mjs",
      url: `${FAKE_URL}/_health`,
      reuseExistingServer: false,
      env: { FAKE_CLAUDE_PORT: "19911" },
    },
    {
      // run-server.mjs builds the binary, clears the db and runs it — one
      // cross-platform launcher instead of a bash-only shell chain.
      command: "node run-server.mjs attherack e2e.db",
      url: APP_URL + "/",
      reuseExistingServer: false,
      timeout: 120_000,
      env: {
        ADDR: "127.0.0.1:18181",
        ANTHROPIC_API_KEY: "sk-ant-e2e",
        ANTHROPIC_BASE_URL: FAKE_URL,
      },
    },
    {
      command: "node run-server.mjs attherack-nokey nokey.db",
      url: NOKEY_URL + "/",
      reuseExistingServer: false,
      timeout: 120_000,
      env: {
        ADDR: "127.0.0.1:18182",
        // Set but empty, so neither the shell's key nor a local .env enables it.
        ANTHROPIC_API_KEY: "",
        ANTHROPIC_BASE_URL: FAKE_URL,
      },
    },
    {
      command: "node run-server.mjs attherack-demo demo.db -seed-demo",
      url: DEMO_URL + "/",
      reuseExistingServer: false,
      timeout: 120_000,
      env: {
        ADDR: "127.0.0.1:18183",
        ANTHROPIC_API_KEY: "sk-ant-e2e",
        ANTHROPIC_BASE_URL: FAKE_URL,
      },
    },
  ],
});
