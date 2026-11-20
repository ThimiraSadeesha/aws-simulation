import { expect, test } from "@playwright/test";
import { registerAndLogin } from "./helpers";

test.describe("IAM", () => {
  test("create an IAM user and an access key (secret shown once)", async ({ page }) => {
    await registerAndLogin(page);

    const userName = `e2e-user-${Date.now().toString(36)}`;
    await page.goto("/iam/users");
    await expect(page.getByRole("heading", { name: "Users" })).toBeVisible();

    await page.getByRole("button", { name: "Create user" }).click();
    const createDialog = page.getByRole("dialog", { name: "Create IAM user" });
    await createDialog.getByLabel("User name").fill(userName);
    await createDialog.getByRole("button", { name: "Create user" }).click();

    // The console navigates to the new user's detail page.
    await expect(page).toHaveURL(new RegExp(`/iam/users/${userName}$`));
    await expect(page.getByRole("heading", { name: userName })).toBeVisible();

    await page.getByRole("tab", { name: "Access keys" }).click();
    await page.getByRole("button", { name: "Create access key" }).click();

    const secretDialog = page.getByRole("dialog", { name: `Access key created for ${userName}` });
    await expect(secretDialog).toBeVisible();
    const keyId = (await secretDialog.getByTestId("secret-Access key ID").innerText()).trim();
    const secret = (await secretDialog.getByTestId("secret-Secret access key").innerText()).trim();
    expect(keyId).toMatch(/^AKIA/);
    expect(secret.length).toBeGreaterThan(0);

    await secretDialog.getByRole("button", { name: "I have saved it" }).click();
    await expect(secretDialog).toBeHidden();

    const table = page.getByRole("table", { name: "Access keys" });
    const row = table.getByRole("row").filter({ hasText: keyId });
    await expect(row).toBeVisible();
    await expect(row.getByText("Active", { exact: true })).toBeVisible();
    // The secret is never displayed again.
    await expect(page.getByText(secret)).toHaveCount(0);
  });
});
