import { expect, test } from "@playwright/test";
import { login, registerAndLogin } from "./helpers";

test.describe("Authentication", () => {
  test("unauthenticated users are redirected to /login", async ({ page }) => {
    await page.goto("/ec2");
    await page.waitForURL(/\/login/);
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    expect(new URL(page.url()).searchParams.get("next")).toBe("/ec2");
  });

  test("register, sign out and sign back in", async ({ page }) => {
    const { username, password } = await registerAndLogin(page);
    await expect(page.getByTestId("simulated-indicator")).toBeVisible();

    await page.getByRole("button", { name: "User menu" }).click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    await page.waitForURL(/\/login/);

    await login(page, username, password);
    await expect(page.getByRole("button", { name: "User menu" })).toContainText(username);
  });

  test("wrong password shows the API error message", async ({ page }) => {
    const { username } = await registerAndLogin(page);
    await page.getByRole("button", { name: "User menu" }).click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    await page.waitForURL(/\/login/);
    await page.getByLabel("Username", { exact: true }).fill(username);
    await page.getByLabel("Password", { exact: true }).fill("definitely-wrong-password");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByTestId("form-error")).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
  });
});
