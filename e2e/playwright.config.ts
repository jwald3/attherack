import { defineConfig, devices } from "@playwright/test";

// The app under test is the real Go binary, built from the repo root and
// pointed at fake-claude.mjs via ANTHROPIC_BASE_URL. Both servers are started
// by Playwright and torn down after the run. The database is recreated per run.
export const APP_URL = "http://127.0.0.1:18181";
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
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      command: "node fake-claude.mjs",
      url: `${FAKE_URL}/_health`,
      reuseExistingServer: false,
      env: { FAKE_CLAUDE_PORT: "19911" },
    },
    {
      command:
        "cd .. && go build -o e2e/.bin/attherack . && rm -f e2e/.bin/e2e.db e2e/.bin/e2e.db-shm e2e/.bin/e2e.db-wal && exec e2e/.bin/attherack",
      url: APP_URL + "/",
      reuseExistingServer: false,
      timeout: 120_000,
      env: {
        ADDR: "127.0.0.1:18181",
        DB_PATH: "e2e/.bin/e2e.db",
        ANTHROPIC_API_KEY: "sk-ant-e2e",
        ANTHROPIC_BASE_URL: FAKE_URL,
      },
    },
  ],
});
