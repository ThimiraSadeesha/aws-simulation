"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, toList } from "@/lib/api/client";
import type { MetricDataResponse, MetricDescriptor, Statistic } from "@/lib/types";

export interface MetricQuery {
  namespace: string;
  metric_name: string;
  /** `Name:Value` (e.g. `InstanceId:i-0abc…`) – the doc's `dimension=` query param format. */
  dimension?: string;
  /** Range in minutes back from now. */
  rangeMinutes: number;
  period: number;
  stat: Statistic;
}

export const TIME_RANGES = [
  { label: "15 minutes", minutes: 15 },
  { label: "1 hour", minutes: 60 },
  { label: "3 hours", minutes: 180 },
  { label: "12 hours", minutes: 720 },
  { label: "1 day", minutes: 1440 },
];

export function useMetricData(q: MetricQuery | null, opts: { refetchInterval?: number | false } = {}) {
  return useQuery({
    queryKey: ["cloudwatch", "metric-data", q],
    enabled: !!q && !!q.namespace && !!q.metric_name,
    placeholderData: keepPreviousData,
    refetchInterval: opts.refetchInterval ?? 30_000,
    queryFn: ({ signal }) => {
      const end = new Date();
      const start = new Date(end.getTime() - q!.rangeMinutes * 60_000);
      return api<MetricDataResponse>("/cloudwatch/metrics", {
        signal,
        query: {
          namespace: q!.namespace,
          metric_name: q!.metric_name,
          dimension: q!.dimension,
          start: start.toISOString(),
          end: end.toISOString(),
          period: q!.period,
          stat: q!.stat,
        },
      });
    },
  });
}

export function useMetricList() {
  return useQuery({
    queryKey: ["cloudwatch", "metrics-list"],
    queryFn: async ({ signal }) => toList<MetricDescriptor>(await api<unknown>("/cloudwatch/metrics/list", { signal })).items,
    staleTime: 60_000,
  });
}
