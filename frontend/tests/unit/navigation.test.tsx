import "../next-navigation.mock";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { server } from "../msw/server";
import { navState, renderWithProviders, routerMock } from "../utils";
import { SidebarNav } from "@/components/shell/sidebar";
import { NAV_ITEMS } from "@/components/shell/nav-items";
import { ServiceNav } from "@/components/shell/service-nav";

vi.mock("@/features/events/events-provider", () => ({
  EventsProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useLiveEvents: () => ({ status: "open", events: [], samples: [], lastEventAt: null, unread: 2, markRead: () => {} }),
}));

const EXPECTED = [
  "Dashboard",
  "EC2",
  "S3",
  "VPC",
  "IAM",
  "Load Balancers",
  "Auto Scaling",
  "CloudWatch",
  "RDS",
  "Lambda",
  "DynamoDB",
  "SQS",
  "SNS",
  "Route 53",
  "CloudFormation",
  "Container Services",
  "Secrets Manager",
  "Training Labs",
  "Documentation",
  "Settings",
];

beforeEach(() => {
  navState.pathname = "/vpc/subnets";
});

describe("navigation", () => {
  it("renders every console section in order with the current one marked", () => {
    renderWithProviders(<SidebarNav />);
    const nav = screen.getByRole("navigation", { name: "Main navigation" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((l) => l.textContent?.replace(/soon$/, ""))).toEqual(EXPECTED);
    expect(within(nav).getByRole("link", { name: /^VPC/ })).toHaveAttribute("aria-current", "page");
    expect(within(nav).getByRole("link", { name: /^EC2/ })).not.toHaveAttribute("aria-current");
    expect(NAV_ITEMS).toHaveLength(EXPECTED.length);
  });

  it("marks services not implemented by the backend as coming soon", () => {
    renderWithProviders(<SidebarNav />);
    const nav = screen.getByRole("navigation", { name: "Main navigation" });
    for (const name of ["RDS", "Lambda", "CloudFormation", "Container Services"]) {
      expect(within(nav).getByRole("link", { name: new RegExp(`^${name}`) })).toHaveTextContent("soon");
    }
    expect(within(nav).getByRole("link", { name: /^S3/ })).not.toHaveTextContent("soon");
  });

  it("service sub-navigation highlights the active section", () => {
    renderWithProviders(
      <ServiceNav
        label="VPC"
        items={[
          { label: "VPCs", href: "/vpc", exact: true },
          { label: "Subnets", href: "/vpc/subnets" },
        ]}
      />,
    );
    expect(screen.getByRole("link", { name: "Subnets" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "VPCs" })).not.toHaveAttribute("aria-current");
  });

  it("console layout shows the shell: account id, live indicator and simulated-environment badge", async () => {
    server.use(
      http.get("*/api/v1/auth/me", () =>
        HttpResponse.json({
          user: { id: 1, username: "alice", email: "", role: "user", account_id: "123456789012", created_at: "2026-11-15T00:00:00Z" },
          csrf_token: "t",
        }),
      ),
      http.post("*/api/v1/auth/logout", () => new HttpResponse(null, { status: 204 })),
    );
    const { AuthProvider } = await import("@/features/auth/auth-context");
    const { default: ConsoleLayout } = await import("@/app/(console)/layout");
    const user = userEvent.setup();
    renderWithProviders(
      <AuthProvider>
        <ConsoleLayout>
          <p>page body</p>
        </ConsoleLayout>
      </AuthProvider>,
    );
    expect(await screen.findByText("page body")).toBeInTheDocument();
    expect(screen.getByLabelText("Account ID")).toHaveTextContent("123456789012");
    expect(screen.getByTestId("simulated-indicator")).toHaveTextContent("Simulated environment — no real cloud resources");
    expect(screen.getByRole("button", { name: /Event stream: Live, 2 new events/ })).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Search resources on this page" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "User menu" }));
    await user.click(await screen.findByRole("menuitem", { name: /Sign out/ }));
    await vi.waitFor(() => expect(routerMock.replace).toHaveBeenCalledWith("/login"));
  });
});
