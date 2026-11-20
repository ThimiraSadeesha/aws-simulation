import { expect, test } from "@playwright/test";
import { registerAndLogin } from "./helpers";

// Runs against a REAL CloudSim stack (E2E_BASE_URL). No network mocking.
test("create a VPC and subnet, then see the subnet in the topology diagram", async ({ page }) => {
  await registerAndLogin(page);
  const suffix = Date.now().toString(36);
  const vpcName = `e2e-vpc-${suffix}`;

  // --- Create VPC ---
  await page.goto("/vpc");
  await page.getByRole("button", { name: "Create VPC" }).first().click();
  const vpcDialog = page.getByRole("dialog", { name: "Create VPC" });
  await vpcDialog.getByLabel("Name tag").fill(vpcName);
  await vpcDialog.getByLabel("IPv4 CIDR block").fill("10.20.0.0/16");
  await vpcDialog.getByRole("button", { name: "Create VPC" }).click();

  // The console navigates to the new VPC's detail page.
  await page.waitForURL(/\/vpc\/vpcs\/vpc-[0-9a-f]+/);
  const vpcId = page.url().match(/vpc-[0-9a-f]+/)![0];
  await expect(page.getByRole("heading", { level: 1 })).toContainText(vpcName);

  // --- Create subnet in that VPC ---
  await page.goto("/vpc/subnets");
  await page.getByRole("button", { name: "Create subnet" }).first().click();
  const subnetDialog = page.getByRole("dialog", { name: "Create subnet" });
  await subnetDialog.getByLabel("VPC").selectOption(vpcId);
  await subnetDialog.getByLabel("Name tag").fill(`e2e-subnet-${suffix}`);
  await subnetDialog.getByLabel("IPv4 CIDR block").fill("10.20.1.0/24");
  await subnetDialog.getByRole("button", { name: "Create subnet" }).click();
  await expect(subnetDialog).toBeHidden();
  await expect(page.getByRole("table", { name: "Subnets" })).toContainText("10.20.1.0/24");

  // --- Topology ---
  await page.goto(`/vpc/vpcs/${vpcId}`);
  await page.getByRole("tab", { name: "Topology" }).click();
  const diagram = page.getByTestId("vpc-topology");
  await expect(diagram).toBeVisible();
  await expect(diagram).toContainText("10.20.1.0/24");
  await expect(diagram).toContainText("10.20.0.0/16");
});
