"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList } from "@/lib/api/client";
import type { ListParams, Queue, QueueMessage } from "@/lib/types";

const enc = encodeURIComponent;

export const sqsKeys = {
  all: ["sqs"] as const,
  queues: (p?: ListParams) => ["sqs", "queues", p ?? {}] as const,
};

/** The doc does not specify the receive response shape: accept `[...]`, `{messages:[...]}` or `{items:[...]}`. */
export function normalizeMessages(data: unknown): QueueMessage[] {
  if (Array.isArray(data)) return data as QueueMessage[];
  const d = (data ?? {}) as { messages?: QueueMessage[]; items?: QueueMessage[] };
  return d.messages ?? d.items ?? [];
}

export function messagesAvailable(q: Queue): number | undefined {
  return q.approximate_number_of_messages ?? q.messages_available;
}

export function useQueues(params: ListParams) {
  return useQuery({
    queryKey: sqsKeys.queues(params),
    queryFn: ({ signal }) => apiList<Queue>("/sqs/queues", params, signal),
    placeholderData: keepPreviousData,
  });
}

export function useAllQueues(enabled = true) {
  return useQuery({ queryKey: ["sqs", "queues", "all"], queryFn: ({ signal }) => apiAll<Queue>("/sqs/queues", {}, signal), enabled });
}

/** There is no documented GET /sqs/queues/{name}; the detail page finds the queue in the list. */
export function useQueue(name: string) {
  return useQuery({
    queryKey: ["sqs", "queue", name],
    queryFn: async ({ signal }) => {
      const items = await apiAll<Queue>("/sqs/queues", { q: name }, signal);
      return items.find((q) => q.name === name) ?? null;
    },
    enabled: !!name,
  });
}

function useSqsMutation<V, R = unknown>(fn: (v: V) => Promise<R>, silent = false) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    meta: silent ? { silent: true } : undefined,
    onSuccess: () => qc.invalidateQueries({ queryKey: sqsKeys.all }),
  });
}

export const useCreateQueue = () => useSqsMutation((name: string) => api<Queue>("/sqs/queues", { method: "POST", body: { name } }), true);
export const useDeleteQueue = () => useSqsMutation((name: string) => api<void>(`/sqs/queues/${enc(name)}`, { method: "DELETE" }));
export const useSendMessage = () =>
  useSqsMutation(
    ({ queue, body, delay_seconds }: { queue: string; body: string; delay_seconds?: number }) =>
      api<unknown>(`/sqs/queues/${enc(queue)}/messages`, {
        method: "POST",
        body: delay_seconds ? { body, delay_seconds } : { body },
      }),
    true,
  );
export const usePurgeQueue = () => useSqsMutation((queue: string) => api<void>(`/sqs/queues/${enc(queue)}/purge`, { method: "POST" }));
export const useDeleteMessage = () =>
  useSqsMutation(({ queue, receipt }: { queue: string; receipt: string }) =>
    api<void>(`/sqs/queues/${enc(queue)}/messages/${enc(receipt)}`, { method: "DELETE" }),
  );

/** Receiving changes server state (visibility timeout) so it is a mutation, not a cached query. */
export function useReceiveMessages() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ queue, max, visibility_timeout }: { queue: string; max: number; visibility_timeout: number }) =>
      normalizeMessages(await api<unknown>(`/sqs/queues/${enc(queue)}/messages`, { query: { max, visibility_timeout } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: sqsKeys.all }),
  });
}
