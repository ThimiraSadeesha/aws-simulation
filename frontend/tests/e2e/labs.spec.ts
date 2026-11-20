import { expect, test } from "@playwright/test";
import { registerAndLogin } from "./helpers";

test.describe("Training labs", () => {
  test("start a lab and validate a step", async ({ page }) => {
    await registerAndLogin(page);
    await page.goto("/labs");
    await expect(page.getByRole("heading", { name: "Training Labs" })).toBeVisible();

    // Open the first beginner lab (or the first lab of any level).
    const beginner = page.getByRole("region", { name: /Beginner/ });
    const firstLab = (await beginner.count()) > 0 ? beginner.getByRole("link").first() : page.getByRole("main").getByRole("link").first();
    await firstLab.click();
    await page.waitForURL(/\/labs\/[^/]+$/);

    await page.getByRole("button", { name: "Start lab" }).click();
    await expect(page.getByRole("button", { name: "Start lab" })).toBeHidden();

    // Terminal is embedded and connects over WebSocket.
    await expect(page.getByTestId("lab-terminal")).toBeVisible();
    await expect(page.getByRole("status").filter({ hasText: "Terminal · Connected" })).toBeVisible({ timeout: 20_000 });

    // Validate the first step: a fresh account normally fails it, but the real API message must be shown.
    await page.getByRole("list", { name: "Lab steps" }).getByRole("button").first().click();
    await page.getByRole("button", { name: "Validate step" }).click();
    const result = page.getByTestId("step-result");
    await expect(result).toBeVisible();
    await expect(result).toContainText(/Passed|Not yet/);
    await expect(result).not.toHaveText(/^\s*(Passed|Not yet)\s*$/); // carries the backend's message
  });

  test("terminal runs cloudsim commands", async ({ page }) => {
    await registerAndLogin(page);
    await page.goto("/labs");
    await page.getByRole("main").getByRole("link").first().click();
    await page.waitForURL(/\/labs\/[^/]+$/);
    await expect(page.getByRole("status").filter({ hasText: "Terminal · Connected" })).toBeVisible({ timeout: 20_000 });
    await page.getByTestId("lab-terminal").click();
    await page.keyboard.type("whoami");
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("lab-terminal")).toContainText(/root|arn:cloudsim|user/i, { timeout: 15_000 });
  });
});
