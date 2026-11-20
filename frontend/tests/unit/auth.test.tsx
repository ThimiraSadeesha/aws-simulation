import "../next-navigation.mock";
import { screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { useQuery } from "@tanstack/react-query";
import { server } from "../msw/server";
import { navState, renderWithProviders, routerMock } from "../utils";
import { AuthProvider, loginRedirectPath, useAuth } from "@/features/auth/auth-context";
import { RequireAuth } from "@/features/auth/require-auth";
import { api, getCsrfToken } from "@/lib/api/client";

const user = { id: 1, username: "alice", email: "", role: "admin", account_id: "123456789012", created_at: "2026-11-15T10:00:00Z" };

beforeEach(() => {
  routerMock.replace.mockReset();
  navState.pathname = "/ec2";
});

function Protected() {
  const { user: u } = useAuth();
  return <p>Welcome {u?.username}</p>;
}

describe("auth redirect", () => {
  it("redirects unauthenticated visitors to /login with a next parameter", async () => {
    server.use(http.get("*/api/v1/auth/me", () => HttpResponse.json({ error: { code: "Unauthenticated", message: "no session" } }, { status: 401 })));
    renderWithProviders(
      <AuthProvider>
        <RequireAuth>
          <Protected />
        </RequireAuth>
      </AuthProvider>,
    );
    await waitFor(() => expect(routerMock.replace).toHaveBeenCalledWith("/login?next=%2Fec2"));
    expect(screen.queryByText(/Welcome/)).not.toBeInTheDocument();
  });

  it("renders the console and keeps the CSRF token in memory when the session is valid", async () => {
    server.use(http.get("*/api/v1/auth/me", () => HttpResponse.json({ user, csrf_token: "csrf-from-me" })));
    renderWithProviders(
      <AuthProvider>
        <RequireAuth>
          <Protected />
        </RequireAuth>
      </AuthProvider>,
    );
    expect(await screen.findByText("Welcome alice")).toBeInTheDocument();
    expect(getCsrfToken()).toBe("csrf-from-me");
    expect(routerMock.replace).not.toHaveBeenCalled();
  });

  it("redirects to /login when any API call returns 401 later (expired session)", async () => {
    server.use(
      http.get("*/api/v1/auth/me", () => HttpResponse.json({ user, csrf_token: "t" })),
      http.get("*/api/v1/ec2/instances", () => HttpResponse.json({ error: { code: "Unauthenticated", message: "session expired" } }, { status: 401 })),
    );
    function Lister() {
      const q = useQuery({ queryKey: ["x"], queryFn: () => api("/ec2/instances") });
      return <p>{q.isError ? "failed" : "loading"}</p>;
    }
    renderWithProviders(
      <AuthProvider>
        <RequireAuth>
          <Lister />
        </RequireAuth>
      </AuthProvider>,
    );
    await waitFor(() => expect(routerMock.replace).toHaveBeenCalledWith("/login?next=%2Fec2"));
  });

  it("shows a retryable error when the backend is unreachable (not a redirect)", async () => {
    server.use(http.get("*/api/v1/auth/me", () => new HttpResponse("upstream down", { status: 502 })));
    renderWithProviders(
      <AuthProvider>
        <RequireAuth>
          <Protected />
        </RequireAuth>
      </AuthProvider>,
    );
    expect(await screen.findByText("Could not contact the CloudSim backend.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(routerMock.replace).not.toHaveBeenCalled();
  });

  it("builds safe login redirect paths", () => {
    expect(loginRedirectPath("/")).toBe("/login");
    expect(loginRedirectPath("/login")).toBe("/login");
    expect(loginRedirectPath("/s3/my-bucket")).toBe("/login?next=%2Fs3%2Fmy-bucket");
  });
});
