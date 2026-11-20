import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

/** Liveness of the console server itself (does not depend on the backend). Used by the Docker HEALTHCHECK. */
export function GET() {
  return NextResponse.json({ status: "ok", service: "cloudsim-console" }, { headers: { "Cache-Control": "no-store" } });
}
