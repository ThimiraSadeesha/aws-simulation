"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList, toList } from "@/lib/api/client";
import type { DynamoItem, DynamoKeyDef, DynamoTable, ListParams } from "@/lib/types";

const t = (name: string) => `/dynamodb/tables/${encodeURIComponent(name)}`;

export function useTables(params: ListParams) {
  return useQuery({
    queryKey: ["dynamodb", "tables", params],
    queryFn: ({ signal }) => apiList<DynamoTable>("/dynamodb/tables", params, signal),
    placeholderData: keepPreviousData,
  });
}

/** The contract has no GET /dynamodb/tables/{name}; the key schema is read from the table list. */
export function useTable(name: string) {
  return useQuery({
    queryKey: ["dynamodb", "table", name],
    queryFn: async ({ signal }) => {
      const all = await apiAll<DynamoTable>("/dynamodb/tables", { q: name }, signal);
      return all.find((x) => x.table_name === name) ?? null;
    },
    enabled: !!name,
  });
}

export function useScan(name: string, limit: number) {
  return useQuery({
    queryKey: ["dynamodb", "table", name, "scan", limit],
    queryFn: async ({ signal }) => toList<DynamoItem>(await api<unknown>(`${t(name)}/scan`, { signal, query: { limit } })).items,
    enabled: !!name,
    placeholderData: keepPreviousData,
  });
}

export function useCreateTable() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { table_name: string; partition_key: DynamoKeyDef; sort_key?: DynamoKeyDef }) =>
      api<DynamoTable>("/dynamodb/tables", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dynamodb"] }),
  });
}

export function useDeleteTable() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api<void>(t(name), { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dynamodb"] }),
  });
}

export function usePutItem(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (item: DynamoItem) => api<unknown>(`${t(name)}/items`, { method: "PUT", body: item }),
    meta: { silent: true },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dynamodb", "table", name] }),
  });
}

export function useDeleteItem(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ pk, sk }: { pk: string; sk?: string }) => api<void>(`${t(name)}/items`, { method: "DELETE", query: { pk, sk } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dynamodb", "table", name] }),
  });
}

/** One-shot reads triggered from forms (GET item / POST query). */
export async function getItem(name: string, pk: string, sk?: string): Promise<DynamoItem | null> {
  const r = await api<DynamoItem | { item: DynamoItem | null }>(`${t(name)}/items`, { query: { pk, sk } });
  if (r && typeof r === "object" && "item" in r && Object.keys(r).length === 1) return (r.item as DynamoItem) ?? null;
  return (r as DynamoItem) ?? null;
}

export async function queryItems(name: string, pk: string, skBeginsWith?: string): Promise<DynamoItem[]> {
  const r = await api<unknown>(`${t(name)}/query`, {
    method: "POST",
    body: skBeginsWith ? { pk, sk_begins_with: skBeginsWith } : { pk },
  });
  return toList<DynamoItem>(r).items;
}
