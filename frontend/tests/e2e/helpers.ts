import { expect, type Page } from "@playwright/test";

/** Unique, valid username (^[a-zA-Z0-9_.-]{3,32}$). */
export function uniqueUsername(prefix = "e2e"): string {
  return `${prefix}_${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.slice(0, 32);
}

/**
 * Registers a fresh user through the UI and leaves the browser signed in on /dashboard.
 * Requires the backend to allow registration (ALLOW_REGISTRATION not false).
 */
export async function registerAndLogin(page: Page): Promise<{ username: string; password: string }> {
  const username = uniqueUsername();
  const password = `Pw-${Math.random().toString(36).slice(2, 10)}-e2e`;
  await page.goto("/register");
  await page.getByLabel("Username", { exact: true }).fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Confirm password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Create account" }).click();
  await page.waitForURL(/\/dashboard/, { timeout: 30_000 });
  await expect(page.getByRole("navigation", { name: "Main navigation" })).toBeVisible();
  return { username, password };
}

export async function login(page: Page, username: string, password: string) {
  await page.goto("/login");
  await page.getByLabel("Username", { exact: true }).fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL(/\/dashboard/, { timeout: 30_000 });
}
