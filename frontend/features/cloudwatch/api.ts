"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiList, toList } from "@/lib/api/client";
import type { Alarm, AlarmHistoryItem, AlarmInput, AuditEntry, CloudEvent, ListParams, LogEvent, LogGroup, LogStream } from "@/lib/types";

export const cwKeys = {
  all: ["cloudwatch"] as const,
  alarms: (p?: ListParams) => ["cloudwatch", "alarms", p ?? {}] as const,
  alarm: (name: string) => ["cloudwatch", "alarm", name] as const,
  alarmHistory: (name: string) => ["cloudwatch", "alarm", name, "history"] as const,
  logGroups: (p?: ListParams) => ["cloudwatch", "log-groups", p ?? {}] as const,
  streams: (group: string) => ["cloudwatch", "log-group", group, "streams"] as const,
  logEvents: (group: string, q: LogEventsQuery) => ["cloudwatch", "log-group", group, "events", q] as const,
};

const alarmPath = (n: string) => `/cloudwatch/alarms/${encodeURIComponent(n)}`;
const groupPath = (g: string) => `/cloudwatch/log-groups/${encodeURIComponent(g)}`;

// ---------------- metrics ----------------
export interface PublishMetricInput {
  namespace: string;
  metric_data: { metric_name: string; dimensions?: Record<string, string>; value: number; unit?: string; ts?: string }[];
}

export function usePublishMetric() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: PublishMetricInput) => api<unknown>("/cloudwatch/metrics", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["cloudwatch", "metrics-list"] });
      void qc.invalidateQueries({ queryKey: ["cloudwatch", "metric-data"] });
    },
  });
}

// ---------------- alarms ----------------
export function useAlarms(params: ListParams) {
  return useQuery({
    queryKey: cwKeys.alarms(params),
    queryFn: ({ signal }) => apiList<Alarm>("/cloudwatch/alarms", params, signal),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
  });
}

export function useAlarm(name: string) {
  return useQuery({
    queryKey: cwKeys.alarm(name),
    queryFn: ({ signal }) => api<Alarm>(alarmPath(name), { signal }),
    enabled: !!name,
    refetchInterval: 30_000,
  });
}

export function useAlarmHistory(name: string) {
  return useQuery({
    queryKey: cwKeys.alarmHistory(name),
    queryFn: async ({ signal }) => toList<AlarmHistoryItem>(await api<unknown>(`${alarmPath(name)}/history`, { signal })).items,
    enabled: !!name,
    refetchInterval: 30_000,
  });
}

export function useCreateAlarm() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AlarmInput) => api<Alarm>("/cloudwatch/alarms", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["cloudwatch", "alarms"] });
      void qc.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
}

export function useDeleteAlarm() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api<void>(alarmPath(name), { method: "DELETE" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: cwKeys.all });
      void qc.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
}

// ---------------- logs ----------------
export function useLogGroups(params: ListParams) {
  return useQuery({
    queryKey: cwKeys.logGroups(params),
    queryFn: ({ signal }) => apiList<LogGroup>("/cloudwatch/log-groups", params, signal),
    placeholderData: keepPreviousData,
  });
}

export function useCreateLogGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; retention_days?: number }) => api<LogGroup>("/cloudwatch/log-groups", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cloudwatch", "log-groups"] }),
  });
}

export function useDeleteLogGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api<void>(groupPath(name), { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cloudwatch", "log-groups"] }),
  });
}

export function streamName(s: LogStream): string {
  return s.name ?? s.stream_name ?? "";
}

export function useLogStreams(group: string) {
  return useQuery({
    queryKey: cwKeys.streams(group),
    queryFn: async ({ signal }) => toList<LogStream>(await api<unknown>(`${groupPath(group)}/streams`, { query: { page_size: 200 }, signal })).items,
    enabled: !!group,
  });
}

export interface LogEventsQuery {
  filter_pattern?: string;
  stream?: string;
  start?: string;
  end?: string;
  limit?: number;
}

export function useLogEvents(group: string, q: LogEventsQuery, refetchInterval: number | false) {
  return useQuery({
    queryKey: cwKeys.logEvents(group, q),
    queryFn: async ({ signal }) => {
      const data = await api<unknown>(`${groupPath(group)}/events`, { query: { ...q }, signal });
      const d = data as { events?: LogEvent[] } | null;
      if (d && !Array.isArray(d) && Array.isArray(d.events)) return d.events;
      return toList<LogEvent>(data).items;
    },
    enabled: !!group,
    placeholderData: keepPreviousData,
    refetchInterval,
  });
}

export function usePutLogEvents(group: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ stream, events }: { stream: string; events: { message: string; ts?: string }[] }) =>
      api<unknown>(`${groupPath(group)}/streams/${encodeURIComponent(stream)}/events`, { method: "POST", body: { events } }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cloudwatch", "log-group", group] }),
  });
}

// ---------------- events & audit ----------------
export function useEvents(params: ListParams) {
  return useQuery({
    queryKey: ["events", "list", params],
    queryFn: ({ signal }) => apiList<CloudEvent>("/events", params, signal),
    placeholderData: keepPreviousData,
  });
}

export function useAudit(params: ListParams) {
  return useQuery({
    queryKey: ["events", "audit", params],
    queryFn: ({ signal }) => apiList<AuditEntry>("/audit", params, signal),
    placeholderData: keepPreviousData,
  });
}
