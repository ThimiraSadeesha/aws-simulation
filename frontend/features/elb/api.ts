"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList, toList } from "@/lib/api/client";
import type { HealthCheck, LbSimulationResult, ListParams, Listener, LoadBalancer, Target, TargetGroup } from "@/lib/types";

export const elbKeys = {
  all: ["elb"] as const,
  lbs: (p?: ListParams) => ["elb", "load-balancers", p ?? {}] as const,
  lb: (id: string) => ["elb", "load-balancer", id] as const,
  tgs: (p?: ListParams) => ["elb", "target-groups", p ?? {}] as const,
  tg: (id: string) => ["elb", "target-group", id] as const,
  tgHealth: (id: string) => ["elb", "target-group", id, "health"] as const,
};

const TRANSITIONAL = new Set(["provisioning", "deleting"]);

export function useLoadBalancers(params: ListParams) {
  return useQuery({
    queryKey: elbKeys.lbs(params),
    queryFn: ({ signal }) => apiList<LoadBalancer>("/elb/load-balancers", params, signal),
    placeholderData: keepPreviousData,
    refetchInterval: (q) => (q.state.data?.items.some((l) => TRANSITIONAL.has(l.state)) ? 2000 : false),
  });
}

export function useLoadBalancer(id: string) {
  return useQuery({
    queryKey: elbKeys.lb(id),
    queryFn: ({ signal }) => api<LoadBalancer>(`/elb/load-balancers/${encodeURIComponent(id)}`, { signal }),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data && TRANSITIONAL.has(q.state.data.state) ? 2000 : false),
  });
}

export function useTargetGroups(params: ListParams) {
  return useQuery({
    queryKey: elbKeys.tgs(params),
    queryFn: ({ signal }) => apiList<TargetGroup>("/elb/target-groups", params, signal),
    placeholderData: keepPreviousData,
  });
}

export function useAllTargetGroups() {
  return useQuery({
    queryKey: ["elb", "target-groups", "all"],
    queryFn: ({ signal }) => apiAll<TargetGroup>("/elb/target-groups", {}, signal),
    staleTime: 15_000,
  });
}

export function useTargetGroup(id: string) {
  return useQuery({
    queryKey: elbKeys.tg(id),
    queryFn: ({ signal }) => api<TargetGroup>(`/elb/target-groups/${encodeURIComponent(id)}`, { signal }),
    enabled: !!id,
  });
}

/** GET /elb/target-groups/{id}/health – accepts `{targets:[…]}`, `{items:[…]}` or a bare array. */
export function useTargetHealth(id: string) {
  return useQuery({
    queryKey: elbKeys.tgHealth(id),
    queryFn: async ({ signal }) => {
      const data = await api<unknown>(`/elb/target-groups/${encodeURIComponent(id)}/health`, { signal });
      const d = data as { targets?: Target[] } | null;
      if (d && !Array.isArray(d) && Array.isArray(d.targets)) return d.targets;
      return toList<Target>(data).items;
    },
    enabled: !!id,
    refetchInterval: 5000,
  });
}

export interface CreateLoadBalancerInput {
  name: string;
  scheme: "internet-facing" | "internal";
  subnet_ids: string[];
  security_group_ids: string[];
}

export function useCreateLoadBalancer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateLoadBalancerInput) => api<LoadBalancer>("/elb/load-balancers", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}

export function useDeleteLoadBalancer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/elb/load-balancers/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}

export function useCreateListener(lbId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { protocol: string; port: number; default_target_group_id: string }) =>
      api<Listener>(`/elb/load-balancers/${encodeURIComponent(lbId)}/listeners`, { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}

export function useDeleteListener(lbId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (listenerId: string) =>
      api<void>(`/elb/load-balancers/${encodeURIComponent(lbId)}/listeners/${encodeURIComponent(listenerId)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}

export function useSimulateTraffic(lbId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { requests: number; listener_port?: number; duration_s?: number }) =>
      api<LbSimulationResult>(`/elb/load-balancers/${encodeURIComponent(lbId)}/simulate`, { method: "POST", body }),
    meta: { silent: true },
    // Simulation raises target CPU → refresh instance / metric views.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["ec2"] });
      void qc.invalidateQueries({ queryKey: ["cloudwatch"] });
    },
  });
}

export interface CreateTargetGroupInput {
  name: string;
  protocol: string;
  port: number;
  vpc_id: string;
  health_check: HealthCheck;
}

export function useCreateTargetGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateTargetGroupInput) => api<TargetGroup>("/elb/target-groups", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}

export function useUpdateTargetGroup(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<Pick<TargetGroup, "algorithm" | "health_check">>) =>
      api<TargetGroup>(`/elb/target-groups/${encodeURIComponent(id)}`, { method: "PATCH", body }),
    onSuccess: (tg) => {
      if (tg?.target_group_id) qc.setQueryData(elbKeys.tg(id), tg);
      void qc.invalidateQueries({ queryKey: elbKeys.all });
    },
  });
}

export function useDeleteTargetGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/elb/target-groups/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}

export function useRegisterTargets(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (targets: { id: string; port?: number; weight?: number }[]) =>
      api<unknown>(`/elb/target-groups/${encodeURIComponent(id)}/targets`, { method: "POST", body: { targets } }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}

export function useDeregisterTarget(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (instanceId: string) =>
      api<void>(`/elb/target-groups/${encodeURIComponent(id)}/targets/${encodeURIComponent(instanceId)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: elbKeys.all }),
  });
}
