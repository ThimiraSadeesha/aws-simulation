import { NextResponse, type NextRequest } from "next/server";

/**
 * Same-origin reverse proxy to the CloudSim backend.
 *
 * BACKEND_INTERNAL_URL is read at request time (not inlined at build time), so a single
 * Docker image can be pointed at any backend via its runtime environment.
 */
export function backendBase(): string {
  const raw = process.env["BACKEND_INTERNAL_URL"] || "http://localhost:8080";
  return raw.replace(/\/+$/, "");
}

export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  const target = new URL(`${backendBase()}${pathname}${search}`);
  return NextResponse.rewrite(target);
}

export const config = {
  matcher: ["/api/:path*", "/healthz"],
};
