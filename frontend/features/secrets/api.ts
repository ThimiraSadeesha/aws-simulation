"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiList } from "@/lib/api/client";
import type { ListParams, Secret } from "@/lib/types";

const path = (name: string) => `/secrets/${encodeURIComponent(name)}`;

export const secretKeys = {
  list: (p: ListParams) => ["secrets", "list", p] as const,
  detail: (name: string) => ["secrets", "detail", name] as const,
};

export function useSecrets(params: ListParams) {
  return useQuery({
    queryKey: secretKeys.list(params),
    queryFn: ({ signal }) => apiList<Secret>("/secrets", params, signal),
    placeholderData: keepPreviousData,
  });
}

export function useSecret(name: string) {
  return useQuery({
    queryKey: secretKeys.detail(name),
    queryFn: ({ signal }) => api<Secret>(path(name), { signal }),
    enabled: !!name,
  });
}

/** The plaintext value is fetched on explicit user action only (never cached; audited server-side). */
export function useRetrieveSecretValue(name: string) {
  return useMutation({
    mutationFn: async () => {
      const r = await api<Secret | string>(`${path(name)}/value`);
      if (typeof r === "string") return r;
      return r?.secret_string ?? r?.value ?? "";
    },
  });
}

export function useCreateSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; secret_string: string; description?: string }) => api<Secret>("/secrets", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });
}

export function useUpdateSecret(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { secret_string: string; description?: string }) => api<Secret>(path(name), { method: "PUT", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });
}

export function useDeleteSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api<void>(path(name), { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });
}
