import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { server } from "../msw/server";
import { api, apiList, ApiError, buildQuery, encodeKey, setCsrfToken, setUnauthorizedHandler, toList } from "@/lib/api/client";

afterEach(() => {
  setCsrfToken(null);
  setUnauthorizedHandler(null);
});

describe("api client", () => {
  it("parses the error envelope into an ApiError with code, message and request id", async () => {
    server.use(
      http.post("*/api/v1/vpc/vpcs", () =>
        HttpResponse.json(
          { error: { code: "ValidationError", message: "cidr_block prefix must be between /16 and /28", request_id: "req-123" } },
          { status: 400 },
        ),
      ),
    );
    const err = await api("/vpc/vpcs", { method: "POST", body: { cidr_block: "10.0.0.0/8" } }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as ApiError;
    expect(e.status).toBe(400);
    expect(e.code).toBe("ValidationError");
    expect(e.message).toBe("cidr_block prefix must be between /16 and /28");
    expect(e.requestId).toBe("req-123");
  });

  it("maps 409 conflict codes such as DependencyViolation", async () => {
    server.use(
      http.delete("*/api/v1/vpc/vpcs/:id", () =>
        HttpResponse.json({ error: { code: "DependencyViolation", message: "VPC has subnets" } }, { status: 409 }),
      ),
    );
    await expect(api("/vpc/vpcs/vpc-1", { method: "DELETE" })).rejects.toMatchObject({
      status: 409,
      code: "DependencyViolation",
      message: "VPC has subnets",
    });
  });

  it("falls back gracefully for non-JSON error bodies", async () => {
    server.use(http.get("*/api/v1/dashboard/summary", () => new HttpResponse("Bad gateway", { status: 502 })));
    await expect(api("/dashboard/summary")).rejects.toMatchObject({ status: 502, message: "The CloudSim backend is not reachable." });
  });

  it("sends X-CSRF-Token on non-GET requests only, with credentials", async () => {
    const seen: { method: string; csrf: string | null; contentType: string | null }[] = [];
    server.use(
      http.all("*/api/v1/ec2/instances", ({ request }) => {
        seen.push({
          method: request.method,
          csrf: request.headers.get("X-CSRF-Token"),
          contentType: request.headers.get("Content-Type"),
        });
        return HttpResponse.json({ items: [], total: 0, page: 1, page_size: 20 });
      }),
    );
    setCsrfToken("tok-abc");
    await api("/ec2/instances");
    await api("/ec2/instances", { method: "POST", body: { image_id: "ami-1", instance_type: "t3.micro" } });
    expect(seen[0]).toEqual({ method: "GET", csrf: null, contentType: null });
    expect(seen[1].method).toBe("POST");
    expect(seen[1].csrf).toBe("tok-abc");
    expect(seen[1].contentType).toBe("application/json");
  });

  it("calls the unauthorized handler on 401 unless skipped", async () => {
    server.use(
      http.get("*/api/v1/ec2/instances", () => HttpResponse.json({ error: { code: "Unauthenticated", message: "no session" } }, { status: 401 })),
    );
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    await expect(api("/ec2/instances")).rejects.toMatchObject({ status: 401, code: "Unauthenticated" });
    expect(handler).toHaveBeenCalledTimes(1);
    await expect(api("/ec2/instances", { skipAuthRedirect: true })).rejects.toBeInstanceOf(ApiError);
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it("returns undefined for 204 responses", async () => {
    server.use(http.delete("*/api/v1/ec2/key-pairs/:name", () => new HttpResponse(null, { status: 204 })));
    await expect(api("/ec2/key-pairs/k1", { method: "DELETE" })).resolves.toBeUndefined();
  });

  it("sends list params and normalises list responses", async () => {
    let url: URL | null = null;
    server.use(
      http.get("*/api/v1/ec2/instances", ({ request }) => {
        url = new URL(request.url);
        return HttpResponse.json({ items: [{ instance_id: "i-1" }], total: 41, page: 3, page_size: 20 });
      }),
    );
    const res = await apiList("/ec2/instances", { q: "web", sort: "name", order: "desc", page: 3, page_size: 20, state: "running", empty: "" });
    expect(res.total).toBe(41);
    expect(res.page).toBe(3);
    const u = url as unknown as URL;
    expect(u.searchParams.get("q")).toBe("web");
    expect(u.searchParams.get("sort")).toBe("name");
    expect(u.searchParams.get("order")).toBe("desc");
    expect(u.searchParams.get("page")).toBe("3");
    expect(u.searchParams.get("state")).toBe("running");
    expect(u.searchParams.has("empty")).toBe(false);
  });

  it("toList accepts bare arrays and envelopes", () => {
    expect(toList([{ a: 1 }, { a: 2 }])).toMatchObject({ total: 2, page: 1 });
    expect(toList({ items: [1], total: 5, page: 2, page_size: 1 })).toEqual({ items: [1], total: 5, page: 2, page_size: 1 });
    expect(toList(undefined).items).toEqual([]);
  });

  it("buildQuery skips empty values and encodeKey keeps slashes", () => {
    expect(buildQuery({ a: 1, b: undefined, c: "", d: "x y" })).toBe("?a=1&d=x+y");
    expect(encodeKey("photos/2026/cat pic.jpg")).toBe("photos/2026/cat%20pic.jpg");
  });
});
