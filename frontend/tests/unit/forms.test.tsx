import "../next-navigation.mock";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { server } from "../msw/server";
import { renderWithProviders, routerMock } from "../utils";
import LaunchPage from "@/app/(console)/ec2/launch/page";
import { CreateVpcDialog } from "@/features/vpc/components/create-vpc-dialog";
import { CreateBucketDialog } from "@/features/s3/bucket-list";
import { CreateAlarmForm } from "@/features/cloudwatch/alarm-form";
import { setCsrfToken } from "@/lib/api/client";

const list = <T,>(items: T[]) => ({ items, total: items.length, page: 1, page_size: 200 });

beforeEach(() => {
  routerMock.push.mockReset();
  routerMock.replace.mockReset();
  setCsrfToken("csrf-test");
});

describe("EC2 launch wizard", () => {
  function mockLookups() {
    server.use(
      http.get("*/api/v1/ec2/images", () =>
        HttpResponse.json([{ image_id: "ami-00000000000000001", name: "cloudsim-linux", os: "linux", description: "Linux", architecture: "x86_64" }]),
      ),
      http.get("*/api/v1/ec2/instance-types", () =>
        HttpResponse.json([{ name: "t3.micro", vcpu: 2, memory_mib: 1024, family: "t3", hourly_price_sim: 0.0104 }]),
      ),
      http.get("*/api/v1/ec2/key-pairs", () => HttpResponse.json(list([]))),
      http.get("*/api/v1/vpc/vpcs", () => HttpResponse.json(list([]))),
      http.get("*/api/v1/vpc/subnets", () => HttpResponse.json(list([]))),
      http.get("*/api/v1/vpc/security-groups", () => HttpResponse.json(list([]))),
    );
  }

  it("blocks submission with inline errors when image/type are missing or count is invalid", async () => {
    mockLookups();
    const posted = vi.fn();
    server.use(http.post("*/api/v1/ec2/instances", () => (posted(), HttpResponse.json({ instances: [] }, { status: 201 }))));
    const user = userEvent.setup();
    renderWithProviders(<LaunchPage />);
    await screen.findByRole("option", { name: /cloudsim-linux/ });
    const count = screen.getByLabelText(/^Number of instances/);
    await user.clear(count);
    await user.type(count, "50");
    await user.click(screen.getByRole("button", { name: /Launch instance/ }));
    const alerts = await screen.findAllByRole("alert");
    const text = alerts.map((a) => a.textContent).join(" ");
    expect(text).toMatch(/Choose an image/);
    expect(text).toMatch(/Choose an instance type/);
    expect(text).toMatch(/from 1 to 20/);
    expect(posted).not.toHaveBeenCalled();
  });

  it("submits a valid request and surfaces API errors inline", async () => {
    mockLookups();
    let body: Record<string, unknown> | null = null;
    let csrf: string | null = null;
    server.use(
      http.post("*/api/v1/ec2/instances", async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        csrf = request.headers.get("X-CSRF-Token");
        return HttpResponse.json({ error: { code: "QuotaExceeded", message: "Instance quota exceeded (max 10)" } }, { status: 429 });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<LaunchPage />);
    await screen.findByRole("option", { name: /cloudsim-linux/ });
    await user.type(screen.getByLabelText(/^Name/), "web-1");
    await user.selectOptions(screen.getByLabelText(/^Image \(AMI\)/), "ami-00000000000000001");
    await user.selectOptions(screen.getByLabelText(/^Instance type/), "t3.micro");
    await user.click(screen.getByRole("button", { name: /Launch instance/ }));
    await waitFor(() => expect(screen.getByTestId("form-error")).toHaveTextContent("Instance quota exceeded (max 10)"));
    expect(body).toMatchObject({ image_id: "ami-00000000000000001", instance_type: "t3.micro", count: 1, name: "web-1" });
    expect(csrf).toBe("csrf-test");
    expect(routerMock.push).not.toHaveBeenCalled();
  });
});

describe("VPC create dialog", () => {
  it("validates the CIDR inline and does not call the API for invalid input", async () => {
    const posted = vi.fn();
    server.use(http.post("*/api/v1/vpc/vpcs", () => (posted(), HttpResponse.json({}, { status: 201 }))));
    const user = userEvent.setup();
    renderWithProviders(<CreateVpcDialog open onOpenChange={() => {}} />);
    const dialog = screen.getByRole("dialog");
    const cidr = within(dialog).getByLabelText(/^IPv4 CIDR block/);
    await user.clear(cidr);
    await user.type(cidr, "10.0.0.0/8");
    await user.click(within(dialog).getByRole("button", { name: "Create VPC" }));
    expect(await within(dialog).findByText(/between \/16 and \/28/)).toBeInTheDocument();
    await user.clear(cidr);
    await user.type(cidr, "10.0.0.7/16");
    expect(await within(dialog).findByText(/Host bits must be zero/)).toBeInTheDocument();
    expect(posted).not.toHaveBeenCalled();
  });

  it("shows the RFC1918 warning but allows public ranges, then posts and navigates", async () => {
    let body: unknown = null;
    server.use(
      http.post("*/api/v1/vpc/vpcs", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(
          { vpc_id: "vpc-0123456789abcdef0", name: "lab", cidr_block: "100.64.0.0/16", is_default: false, state: "available", tags: {} },
          { status: 201 },
        );
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<CreateVpcDialog open onOpenChange={() => {}} />);
    const dialog = screen.getByRole("dialog");
    await user.type(within(dialog).getByLabelText(/^Name tag/), "lab");
    const cidr = within(dialog).getByLabelText(/^IPv4 CIDR block/);
    await user.clear(cidr);
    await user.type(cidr, "100.64.0.0/16");
    expect(within(dialog).getByText(/Not an RFC 1918 private range/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Create VPC" }));
    await waitFor(() => expect(routerMock.push).toHaveBeenCalledWith("/vpc/vpcs/vpc-0123456789abcdef0"));
    expect(body).toMatchObject({ cidr_block: "100.64.0.0/16", name: "lab" });
  });
});

describe("S3 create bucket dialog", () => {
  it("shows bucket-name rule errors and the server error message", async () => {
    server.use(
      http.post("*/api/v1/s3/buckets", () =>
        HttpResponse.json({ error: { code: "Conflict", message: "Bucket name already taken" } }, { status: 409 }),
      ),
    );
    const user = userEvent.setup();
    renderWithProviders(<CreateBucketDialog open onOpenChange={() => {}} />);
    const dialog = screen.getByRole("dialog");
    const name = within(dialog).getByLabelText(/^Bucket name/);
    await user.type(name, "My_Bucket");
    await user.click(within(dialog).getByRole("button", { name: /Create bucket/ }));
    expect(await within(dialog).findByText(/lowercase letters, digits, dots/)).toBeInTheDocument();
    await user.clear(name);
    await user.type(name, "taken-bucket");
    await user.click(within(dialog).getByRole("button", { name: /Create bucket/ }));
    expect(await within(dialog).findByText("Bucket name already taken")).toBeInTheDocument();
  });
});

describe("CloudWatch alarm form", () => {
  it("reports validation errors and does not submit", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderWithProviders(<CreateAlarmForm onSubmit={onSubmit} />);
    await user.clear(screen.getByLabelText("Metric name"));
    await user.clear(screen.getByLabelText("Period (seconds)"));
    await user.type(screen.getByLabelText("Period (seconds)"), "45");
    await user.clear(screen.getByLabelText("Threshold"));
    await user.type(screen.getByLabelText("Threshold"), "high");
    await user.click(screen.getByRole("button", { name: "Create alarm" }));
    const alerts = (await screen.findAllByRole("alert")).map((a) => a.textContent).join(" ");
    expect(alerts).toMatch(/Alarm name is required/);
    expect(alerts).toMatch(/Metric name is required/);
    expect(alerts).toMatch(/multiple of 60/);
    expect(alerts).toMatch(/Threshold must be a number/);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("submits a typed AlarmInput when valid", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderWithProviders(<CreateAlarmForm onSubmit={onSubmit} />);
    await user.type(screen.getByLabelText("Alarm name"), "high-cpu");
    await user.clear(screen.getByLabelText("Threshold"));
    await user.type(screen.getByLabelText("Threshold"), "75");
    await user.click(screen.getByRole("button", { name: "Create alarm" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      alarm_name: "high-cpu",
      namespace: "CloudSim/EC2",
      metric_name: "CPUUtilization",
      threshold: 75,
      period: 60,
      evaluation_periods: 1,
    });
  });
});
