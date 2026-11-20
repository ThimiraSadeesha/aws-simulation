import { expect, test } from "@playwright/test";
import { registerAndLogin } from "./helpers";

test.describe("EC2", () => {
  test("launch an instance, see it running, then stop it", async ({ page }) => {
    await registerAndLogin(page);
    const name = `e2e-web-${Date.now().toString(36)}`;

    await page.goto("/ec2/launch");
    await expect(page.getByRole("heading", { name: "Launch instances" })).toBeVisible();
    await page.getByLabel("Name", { exact: true }).fill(name);

    const image = page.getByLabel("Image (AMI)");
    await expect(image.locator("option").nth(1)).toBeAttached();
    await image.selectOption({ index: 1 });

    const type = page.getByLabel("Instance type");
    await expect(type.locator("option").nth(1)).toBeAttached();
    await type.selectOption({ index: 1 });

    await page.getByRole("button", { name: "Launch instance" }).click();

    // count = 1 → navigates to the instance detail page
    await page.waitForURL(/\/ec2\/instances\/i-[0-9a-f]+/);
    await expect(page.getByRole("heading", { name: new RegExp(name) })).toBeVisible();
    await expect(page.getByRole("heading", { name: new RegExp(name) }).getByText("running", { exact: true })).toBeVisible({ timeout: 60_000 });

    await page.getByRole("button", { name: "Stop", exact: true }).click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog.getByText("Stop instance?")).toBeVisible();
    await dialog.getByRole("button", { name: "Stop", exact: true }).click();

    await expect(page.getByRole("heading", { name: new RegExp(name) }).getByText("stopped", { exact: true })).toBeVisible({ timeout: 60_000 });

    // The list shows the instance as stopped too.
    await page.goto("/ec2");
    const row = page.getByRole("row", { name: new RegExp(name) });
    await expect(row.getByText("stopped", { exact: true })).toBeVisible();
  });
});
