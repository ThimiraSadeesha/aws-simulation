"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList, toList } from "@/lib/api/client";
import type {
  CloudEvent,
  ConsoleOutput,
  CreatedKeyPair,
  Instance,
  InstanceType,
  KeyPair,
  ListParams,
  MachineImage,
  RunInstancesRequest,
  Tags,
  WorkloadProfile,
} from "@/lib/types";
import { TRANSITIONAL_STATES } from "@/components/console/status-badge";

export const ec2Keys = {
  all: ["ec2"] as const,
  instances: (p?: ListParams) => ["ec2", "instances", p ?? {}] as const,
  instance: (id: string) => ["ec2", "instance", id] as const,
  consoleOutput: (id: string) => ["ec2", "instance", id, "console"] as const,
  metadata: (id: string) => ["ec2", "instance", id, "metadata"] as const,
  history: (id: string) => ["ec2", "instance", id, "history"] as const,
  types: ["ec2", "instance-types"] as const,
  images: ["ec2", "images"] as const,
  keyPairs: (p?: ListParams) => ["ec2", "key-pairs", p ?? {}] as const,
};

const POLL_MS = 2000;

export function useInstances(params: ListParams) {
  return useQuery({
    queryKey: ec2Keys.instances(params),
    queryFn: ({ signal }) => apiList<Instance>("/ec2/instances", params, signal),
    placeholderData: keepPreviousData,
    // Poll while any visible instance is in a transitional state (pending, stopping, …).
    refetchInterval: (q) => (q.state.data?.items.some((i) => TRANSITIONAL_STATES.has(i.state)) ? POLL_MS : false),
  });
}

export function useAllInstances(query: Record<string, string> = {}) {
  return useQuery({
    queryKey: ["ec2", "instances", "all", query],
    queryFn: ({ signal }) => apiAll<Instance>("/ec2/instances", query, signal),
    staleTime: 10_000,
  });
}

export function useInstance(id: string) {
  return useQuery({
    queryKey: ec2Keys.instance(id),
    queryFn: ({ signal }) => api<Instance>(`/ec2/instances/${encodeURIComponent(id)}`, { signal }),
    refetchInterval: (q) => (q.state.data && TRANSITIONAL_STATES.has(q.state.data.state) ? POLL_MS : false),
    enabled: !!id,
  });
}

export function useConsoleOutput(id: string, enabled = true) {
  return useQuery({
    queryKey: ec2Keys.consoleOutput(id),
    queryFn: ({ signal }) => api<ConsoleOutput>(`/ec2/instances/${encodeURIComponent(id)}/console-output`, { signal }),
    enabled,
  });
}

export function useInstanceMetadata(id: string, enabled = true) {
  return useQuery({
    queryKey: ec2Keys.metadata(id),
    queryFn: ({ signal }) => api<Record<string, unknown>>(`/ec2/instances/${encodeURIComponent(id)}/metadata`, { signal }),
    enabled,
  });
}

export function useInstanceHistory(id: string, enabled = true) {
  return useQuery({
    queryKey: ec2Keys.history(id),
    queryFn: async ({ signal }) => toList<CloudEvent>(await api<unknown>(`/ec2/instances/${encodeURIComponent(id)}/history`, { signal })).items,
    enabled,
  });
}

export function useInstanceTypes() {
  return useQuery({
    queryKey: ec2Keys.types,
    queryFn: async ({ signal }) => toList<InstanceType>(await api<unknown>("/ec2/instance-types", { signal })).items,
    staleTime: 5 * 60_000,
  });
}

export function useImages() {
  return useQuery({
    queryKey: ec2Keys.images,
    queryFn: async ({ signal }) => toList<MachineImage>(await api<unknown>("/ec2/images", { signal })).items,
    staleTime: 5 * 60_000,
  });
}

export function useKeyPairs(params: ListParams = { page_size: 200 }) {
  return useQuery({
    queryKey: ec2Keys.keyPairs(params),
    queryFn: ({ signal }) => apiList<KeyPair>("/ec2/key-pairs", params, signal),
    placeholderData: keepPreviousData,
  });
}

// ---------------- mutations ----------------

export function useRunInstances() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: RunInstancesRequest) => api<{ instances: Instance[] }>("/ec2/instances", { method: "POST", body }),
    meta: { silent: true }, // the launch wizard shows the error inline
    onSuccess: () => qc.invalidateQueries({ queryKey: ec2Keys.all }),
  });
}

export type InstanceAction = "start" | "stop" | "reboot" | "terminate";

export function useInstanceAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: InstanceAction }) =>
      api<Instance>(`/ec2/instances/${encodeURIComponent(id)}/${action}`, { method: "POST" }),
    onSuccess: (inst) => {
      // Safe optimistic-ish update: the server returned the transitional state.
      if (inst?.instance_id) qc.setQueryData(ec2Keys.instance(inst.instance_id), inst);
      void qc.invalidateQueries({ queryKey: ec2Keys.all });
      void qc.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
}

export function useUpdateInstanceTags(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (tags: Tags) => api<Instance>(`/ec2/instances/${encodeURIComponent(id)}/tags`, { method: "PUT", body: { tags } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ec2Keys.all }),
  });
}

export function useUpdateWorkload(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { profile: WorkloadProfile; cpu_target?: number }) =>
      api<Instance>(`/ec2/instances/${encodeURIComponent(id)}/workload`, { method: "PUT", body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ec2Keys.instance(id) }),
  });
}

export function useUpdateInstanceSecurityGroups(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (security_group_ids: string[]) =>
      api<Instance>(`/ec2/instances/${encodeURIComponent(id)}/security-groups`, { method: "PUT", body: { security_group_ids } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ec2Keys.all }),
  });
}

export function useCreateKeyPair() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api<CreatedKeyPair>("/ec2/key-pairs", { method: "POST", body: { name } }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ec2", "key-pairs"] }),
  });
}

export function useDeleteKeyPair() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api<void>(`/ec2/key-pairs/${encodeURIComponent(name)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ec2", "key-pairs"] }),
  });
}
