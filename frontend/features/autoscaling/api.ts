"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList, toList } from "@/lib/api/client";
import type { AutoScalingGroup, LaunchTemplate, ListParams, ScalingActivity, ScalingPolicy, WorkloadProfile } from "@/lib/types";

export const asgKeys = {
  all: ["autoscaling"] as const,
  templates: (p?: ListParams) => ["autoscaling", "launch-templates", p ?? {}] as const,
  groups: (p?: ListParams) => ["autoscaling", "groups", p ?? {}] as const,
  group: (name: string) => ["autoscaling", "group", name] as const,
  policies: (name: string) => ["autoscaling", "group", name, "policies"] as const,
  activities: (name: string) => ["autoscaling", "group", name, "activities"] as const,
};

const g = (name: string) => `/autoscaling/groups/${encodeURIComponent(name)}`;

export function useLaunchTemplates(params: ListParams) {
  return useQuery({
    queryKey: asgKeys.templates(params),
    queryFn: ({ signal }) => apiList<LaunchTemplate>("/autoscaling/launch-templates", params, signal),
    placeholderData: keepPreviousData,
  });
}

export function useAllLaunchTemplates() {
  return useQuery({
    queryKey: ["autoscaling", "launch-templates", "all"],
    queryFn: ({ signal }) => apiAll<LaunchTemplate>("/autoscaling/launch-templates", {}, signal),
  });
}

export function useGroups(params: ListParams) {
  return useQuery({
    queryKey: asgKeys.groups(params),
    queryFn: ({ signal }) => apiList<AutoScalingGroup>("/autoscaling/groups", params, signal),
    placeholderData: keepPreviousData,
    refetchInterval: 15_000,
  });
}

export function useGroup(name: string) {
  return useQuery({
    queryKey: asgKeys.group(name),
    queryFn: ({ signal }) => api<AutoScalingGroup>(g(name), { signal }),
    enabled: !!name,
    // Instances move through pending/shutting-down while the group converges on desired capacity.
    refetchInterval: (q) =>
      q.state.data?.instances?.some((i) => ["pending", "shutting-down", "stopping"].includes(i.state)) ||
      (q.state.data && (q.state.data.instances?.filter((i) => i.state !== "terminated").length ?? 0) !== q.state.data.desired_capacity)
        ? 3000
        : 15_000,
  });
}

export function usePolicies(name: string) {
  return useQuery({
    queryKey: asgKeys.policies(name),
    queryFn: async ({ signal }) => toList<ScalingPolicy>(await api<unknown>(`${g(name)}/policies`, { signal })).items,
    enabled: !!name,
  });
}

export function useActivities(name: string) {
  return useQuery({
    queryKey: asgKeys.activities(name),
    queryFn: async ({ signal }) => toList<ScalingActivity>(await api<unknown>(`${g(name)}/activities`, { signal })).items,
    enabled: !!name,
    refetchInterval: 10_000,
  });
}

export interface CreateLaunchTemplateInput {
  name: string;
  image_id: string;
  instance_type: string;
  security_group_ids?: string[];
  key_name?: string;
  user_data?: string;
  workload_profile?: WorkloadProfile;
}

export function useCreateLaunchTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateLaunchTemplateInput) => api<LaunchTemplate>("/autoscaling/launch-templates", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["autoscaling", "launch-templates"] }),
  });
}

export interface CreateGroupInput {
  name: string;
  launch_template: string;
  min_size: number;
  max_size: number;
  desired_capacity: number;
  subnet_ids: string[];
  target_group_ids?: string[];
  cooldown_s?: number;
  health_check_type?: "EC2" | "ELB";
}

export function useCreateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateGroupInput) => api<AutoScalingGroup>("/autoscaling/groups", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: asgKeys.all });
      void qc.invalidateQueries({ queryKey: ["ec2"] });
    },
  });
}

export function useUpdateCapacity(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { min_size?: number; max_size?: number; desired_capacity?: number }) =>
      api<AutoScalingGroup>(g(name), { method: "PATCH", body }),
    meta: { silent: true },
    onSuccess: (grp) => {
      if (grp?.name) qc.setQueryData(asgKeys.group(name), grp);
      void qc.invalidateQueries({ queryKey: asgKeys.all });
    },
  });
}

export function useDeleteGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, force }: { name: string; force: boolean }) =>
      api<void>(g(name), { method: "DELETE", query: force ? { force: "true" } : undefined }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: asgKeys.all });
      void qc.invalidateQueries({ queryKey: ["ec2"] });
    },
  });
}

export function useCreatePolicy(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ScalingPolicy) => api<ScalingPolicy>(`${g(name)}/policies`, { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: asgKeys.policies(name) }),
  });
}

export function useDeletePolicy(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (policy: string) => api<void>(`${g(name)}/policies/${encodeURIComponent(policy)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: asgKeys.policies(name) }),
  });
}

/** Result of POST …/evaluate. The doc does not pin the shape; the engine decision is `{desired, change, reason, replace}`. */
export interface EvaluateResult {
  desired?: number;
  change?: number;
  reason?: string;
  replace?: string[];
  [k: string]: unknown;
}

export function useEvaluate(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<EvaluateResult>(`${g(name)}/evaluate`, { method: "POST" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: asgKeys.all });
      void qc.invalidateQueries({ queryKey: ["ec2"] });
    },
  });
}
