# CloudSim Console (frontend)

Next.js (App Router, standalone output) + React + TypeScript + Tailwind + shadcn/ui + TanStack Query.

```bash
npm install
npm run dev          # http://localhost:3000, backend expected at BACKEND_INTERNAL_URL (default http://localhost:8080)
npm run lint && npm run typecheck && npm test && npm run build
E2E_BASE_URL=http://localhost:3000 npx playwright test   # needs a REAL running stack
```

## API proxy
The browser only talks to the console origin. `proxy.ts` forwards `/api/*` and `/healthz` to
`BACKEND_INTERNAL_URL`, read **at request time**, so one image works with any backend. This covers
WebSocket upgrades (`/api/v1/ws/events`, `/api/v1/ws/terminal`) too. `next.config.ts` rewrites are a
fallback that use the build-time value.

## WebSockets
Default: same origin (`ws(s)://<console-host>/api/v1/ws/...`). If your deployment cannot proxy WS
upgrades, set `NEXT_PUBLIC_WS_URL` to the backend origin (e.g. `ws://localhost:8080`). It is inlined at
build time and also served at runtime by `GET /runtime-config`, so a prebuilt image can be reconfigured.
After connecting, the client sends `{"csrf_token": "..."}` first, as the API requires.

## Layout
`app/` routes · `components/ui` shadcn · `components/console` shared (ResourceTable, forms, dialogs, charts) ·
`components/shell` navigation · `features/<service>` API hooks + service components · `hooks/` · `lib/` API
client, types, validation · `tests/unit` Vitest + RTL (MSW) · `tests/e2e` Playwright.

Monaco is served locally from `public/monaco` (copied by `scripts/copy-monaco.mjs` on dev/build), so no CDN is used.
