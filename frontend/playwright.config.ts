import { defineConfig, devices } from "@playwright/test";

/**
 * End-to-end tests run against a REAL running CloudSim stack (frontend + Go backend + engine).
 * Start the stack first (e.g. docker compose up), then: E2E_BASE_URL=http://localhost:3000 npx playwright test
 */
export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: process.env.E2E_BASE_URL || "http://localhost:3000",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    acceptDownloads: true,
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
