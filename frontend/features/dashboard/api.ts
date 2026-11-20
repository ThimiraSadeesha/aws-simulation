"use client";

import { useQuery } from "@tanstack/react-query";
import { api, ApiError, parseError } from "@/lib/api/client";
import type { DashboardSummary, HealthResponse } from "@/lib/types";

export function useDashboardSummary() {
  return useQuery({
    queryKey: ["dashboard", "summary"],
    queryFn: ({ signal }) => api<DashboardSummary>("/dashboard/summary", { signal }),
    refetchInterval: 15_000,
  });
}

/** GET /healthz (no auth, not under /api/v1). */
export function useHealthz() {
  return useQuery({
    queryKey: ["dashboard", "healthz"],
    refetchInterval: 30_000,
    queryFn: async ({ signal }) => {
      let res: Response;
      try {
        res = await fetch("/healthz", { signal, headers: { Accept: "application/json" } });
      } catch {
        throw new ApiError(0, "NetworkError", "Health endpoint unreachable");
      }
      if (!res.ok) throw await parseError(res);
      return (await res.json()) as HealthResponse;
    },
  });
}
