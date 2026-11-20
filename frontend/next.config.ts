import type { NextConfig } from "next";

/**
 * Backend location. Browsers only ever talk to this Next.js server (same origin);
 * requests under /api/* and /healthz are forwarded to the Go backend.
 *
 * Two layers make sure the *runtime* value of BACKEND_INTERNAL_URL is honoured:
 *  1. `proxy.ts` (Next 16 "proxy", formerly middleware) rewrites /api/* and /healthz to
 *     process.env.BACKEND_INTERNAL_URL, read on every request at runtime (Node.js runtime).
 *     This also covers WebSocket upgrades (/api/v1/ws/*).
 *  2. The static rewrites below are kept as a fallback. Note that Next.js serialises these
 *     into the build output, so they use the value present at *build* time.
 */
const backend = process.env.BACKEND_INTERNAL_URL || "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  reactStrictMode: true,
  poweredByHeader: false,
  experimental: {
    // S3 console uploads can be up to MAX_OBJECT_SIZE (100 MiB by default) and pass through proxy.ts.
    proxyClientMaxBodySize: "110mb",
  },
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${backend}/api/:path*` },
      { source: "/healthz", destination: `${backend}/healthz` },
    ];
  },
};

export default nextConfig;
