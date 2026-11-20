/**
 * WebSocket URL resolution.
 *
 * Default: same origin as the console (`ws(s)://<host>/api/v1/ws/...`), forwarded to the backend by proxy.ts.
 * Override: NEXT_PUBLIC_WS_URL = backend origin (e.g. `ws://localhost:8080`) for setups where the
 * Next.js server cannot proxy WebSocket upgrades. It is read at build time (inlined) *and* at runtime
 * via GET /runtime-config, so prebuilt images can be reconfigured.
 */
let runtimeBase: Promise<string | null> | null = null;

function fetchRuntimeBase(): Promise<string | null> {
  if (!runtimeBase) {
    runtimeBase = fetch("/runtime-config", { cache: "no-store" })
      .then((r) => (r.ok ? (r.json() as Promise<{ ws_url?: string | null }>) : { ws_url: null }))
      .then((d) => d.ws_url ?? null)
      .catch(() => null);
  }
  return runtimeBase;
}

export function wsUrlFrom(base: string | null | undefined, path: string, loc: { protocol: string; host: string }): string {
  const origin = base ? base.replace(/\/+$/, "") : `${loc.protocol === "https:" ? "wss:" : "ws:"}//${loc.host}`;
  return `${origin}/api/v1${path}`;
}

export async function resolveWsUrl(path: string): Promise<string> {
  const runtime = await fetchRuntimeBase();
  const base = runtime || process.env.NEXT_PUBLIC_WS_URL || null;
  return wsUrlFrom(base, path, window.location);
}
