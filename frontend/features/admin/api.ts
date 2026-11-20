"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, toList } from "@/lib/api/client";
import type { Backup, EngineStats, Fault, IntegrityResult, User } from "@/lib/types";

export const adminKeys = {
  users: ["admin", "users"] as const,
  engine: ["admin", "engine"] as const,
  backups: ["admin", "backups"] as const,
  faults: ["admin", "faults"] as const,
};

export function useAdminUsers(enabled: boolean) {
  return useQuery({
    queryKey: adminKeys.users,
    queryFn: async ({ signal }) => toList<User>(await api<unknown>("/admin/users", { signal, query: { page_size: 200 } })).items,
    enabled,
  });
}

export function useSetUserRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, role }: { id: number; role: User["role"] }) => api<User>(`/admin/users/${id}`, { method: "PATCH", body: { role } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: adminKeys.users }),
  });
}

export function useEngineStats(enabled: boolean) {
  return useQuery({
    queryKey: adminKeys.engine,
    queryFn: ({ signal }) => api<EngineStats>("/admin/engine", { signal }),
    enabled,
    refetchInterval: 10_000,
  });
}

export function useBackups(enabled: boolean) {
  return useQuery({
    queryKey: adminKeys.backups,
    queryFn: async ({ signal }) => toList<Backup>(await api<unknown>("/admin/backups", { signal })).items,
    enabled,
  });
}

export function useCreateBackup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<Backup>("/admin/backups", { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: adminKeys.backups }),
  });
}

export function useRestoreBackup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (file: string) => api<unknown>("/admin/restore", { method: "POST", body: { file } }),
    onSuccess: () => qc.invalidateQueries(),
  });
}

export function useIntegrityCheck() {
  return useMutation({
    mutationFn: () => api<IntegrityResult>("/admin/integrity"),
  });
}

export function useFaults(enabled: boolean) {
  return useQuery({
    queryKey: adminKeys.faults,
    queryFn: async ({ signal }) => toList<Fault>(await api<unknown>("/admin/faults", { signal })).items,
    enabled,
    refetchInterval: 10_000,
  });
}

export interface NewFault {
  target: string | null;
  kind: string | null;
  type: Fault["type"];
  probability: number;
  duration_ms: number | null;
}

export function useCreateFault() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (f: NewFault) => api<Fault>("/admin/faults", { method: "POST", body: f }),
    meta: { silent: true },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: adminKeys.faults });
      void qc.invalidateQueries({ queryKey: adminKeys.engine });
    },
  });
}

export function useDeleteFault() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/admin/faults/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: adminKeys.faults }),
  });
}
