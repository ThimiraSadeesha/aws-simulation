import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";
import { registerAndLogin } from "./helpers";

test.describe("S3", () => {
  test("create bucket, upload, download and delete an object", async ({ page }) => {
    await registerAndLogin(page);
    const bucket = `e2e-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
    const fileName = "e2e-hello.txt";
    const content = `hello from cloudsim e2e ${bucket}\n`;

    // Create the bucket
    await page.goto("/s3");
    await page.getByRole("button", { name: "Create bucket" }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Bucket name").fill(bucket);
    await dialog.getByRole("button", { name: "Create bucket" }).click();
    await expect(dialog).toBeHidden();

    // Open it
    await page.getByRole("link", { name: bucket, exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name: bucket })).toBeVisible();

    // Upload a small file through the (visually hidden) file input
    await page.getByLabel("Upload files").setInputFiles({ name: fileName, mimeType: "text/plain", buffer: Buffer.from(content) });
    await expect(page.getByRole("status").filter({ hasText: "Uploaded 1 object" })).toBeVisible();
    await expect(page.getByRole("button", { name: `Object ${fileName} details` })).toBeVisible();

    // Download it and check the bytes
    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("link", { name: `Download ${fileName}` }).click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe(fileName);
    const path = await download.path();
    expect(await readFile(path, "utf8")).toBe(content);

    // Delete it
    await page.getByRole("button", { name: `Delete ${fileName}` }).click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Delete", exact: true }).click();
    await expect(page.getByRole("button", { name: `Object ${fileName} details` })).toHaveCount(0);
    await expect(page.getByText("This bucket is empty")).toBeVisible();
  });
});
