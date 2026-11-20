import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

/**
 * Runtime (not build-time) browser configuration.
 * NEXT_PUBLIC_WS_URL is normally inlined at build; reading it here as well lets a prebuilt image
 * be pointed at a different WebSocket endpoint via its runtime environment.
 */
export function GET() {
  return NextResponse.json(
    { ws_url: process.env["NEXT_PUBLIC_WS_URL"] || process.env["CLOUDSIM_WS_URL"] || null },
    { headers: { "Cache-Control": "no-store" } },
  );
}
