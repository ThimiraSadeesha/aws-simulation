"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiList } from "@/lib/api/client";
import type { DnsRecord, HostedZone, ListParams } from "@/lib/types";

const enc = encodeURIComponent;

/** The doc names the zone id field ambiguously (`{id}` in paths); accept `id` or `zone_id`. */
export const zoneId = (z: HostedZone) => z.id ?? z.zone_id ?? "";

export const r53Keys = {
  all: ["route53"] as const,
  zones: (p?: ListParams) => ["route53", "zones", p ?? {}] as const,
  zone: (id: string) => ["route53", "zone", id] as const,
  records: (id: string, p?: ListParams) => ["route53", "zone", id, "records", p ?? {}] as const,
};

export function useZones(params: ListParams) {
  return useQuery({
    queryKey: r53Keys.zones(params),
    queryFn: ({ signal }) => apiList<HostedZone>("/route53/zones", params, signal),
    placeholderData: keepPreviousData,
  });
}

/** No GET /route53/zones/{id} is documented — find the zone in the list. */
export function useZone(id: string) {
  return useQuery({
    queryKey: r53Keys.zone(id),
    queryFn: async ({ signal }) => {
      const list = await apiList<HostedZone>("/route53/zones", { page_size: 200 }, signal);
      return list.items.find((z) => zoneId(z) === id) ?? null;
    },
    enabled: !!id,
  });
}

export function useRecords(id: string, params: ListParams) {
  return useQuery({
    queryKey: r53Keys.records(id, params),
    queryFn: ({ signal }) => apiList<DnsRecord>(`/route53/zones/${enc(id)}/records`, params, signal),
    placeholderData: keepPreviousData,
    enabled: !!id,
  });
}

function useInvalidate() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: r53Keys.all });
}

export function useCreateZone() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (body: { name: string; private: boolean; vpc_id?: string }) =>
      api<HostedZone>("/route53/zones", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: invalidate,
  });
}

export function useDeleteZone() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/route53/zones/${enc(id)}`, { method: "DELETE" }),
    onSuccess: invalidate,
  });
}

export function useCreateRecord(id: string) {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (body: DnsRecord) => api<DnsRecord>(`/route53/zones/${enc(id)}/records`, { method: "POST", body }),
    meta: { silent: true },
    onSuccess: invalidate,
  });
}

export function useDeleteRecord(id: string) {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (r: { name: string; type: string }) =>
      api<void>(`/route53/zones/${enc(id)}/records`, { method: "DELETE", query: { name: r.name, type: r.type } }),
    onSuccess: invalidate,
  });
}
