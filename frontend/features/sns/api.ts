"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList } from "@/lib/api/client";
import type { ListParams, LogGroup, Topic } from "@/lib/types";

const enc = encodeURIComponent;

export const snsKeys = {
  all: ["sns"] as const,
  topics: (p?: ListParams) => ["sns", "topics", p ?? {}] as const,
};

export function useTopics(params: ListParams) {
  return useQuery({
    queryKey: snsKeys.topics(params),
    queryFn: ({ signal }) => apiList<Topic>("/sns/topics", params, signal),
    placeholderData: keepPreviousData,
  });
}

/** No GET /sns/topics/{name} is documented; the detail view finds the topic (incl. subscriptions) in the list. */
export function useTopic(name: string) {
  return useQuery({
    queryKey: ["sns", "topic", name],
    queryFn: async ({ signal }) => {
      const items = await apiAll<Topic>("/sns/topics", { q: name }, signal);
      return items.find((t) => t.name === name) ?? null;
    },
    enabled: !!name,
  });
}

export function useLogGroupOptions(enabled = true) {
  return useQuery({
    queryKey: ["cloudwatch", "log-groups", "all"],
    queryFn: ({ signal }) => apiAll<LogGroup>("/cloudwatch/log-groups", {}, signal),
    enabled,
  });
}

function useSnsMutation<V, R = unknown>(fn: (v: V) => Promise<R>, silent = false) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    meta: silent ? { silent: true } : undefined,
    onSuccess: () => qc.invalidateQueries({ queryKey: snsKeys.all }),
  });
}

export const useCreateTopic = () => useSnsMutation((name: string) => api<Topic>("/sns/topics", { method: "POST", body: { name } }), true);
export const useDeleteTopic = () => useSnsMutation((name: string) => api<void>(`/sns/topics/${enc(name)}`, { method: "DELETE" }));
export const useSubscribe = () =>
  useSnsMutation(
    ({ topic, protocol, endpoint }: { topic: string; protocol: "sqs" | "log"; endpoint: string }) =>
      api<unknown>(`/sns/topics/${enc(topic)}/subscriptions`, { method: "POST", body: { protocol, endpoint } }),
    true,
  );
export const usePublish = () =>
  useSnsMutation(
    ({ topic, message, subject }: { topic: string; message: string; subject?: string }) =>
      api<unknown>(`/sns/topics/${enc(topic)}/publish`, { method: "POST", body: subject ? { message, subject } : { message } }),
    true,
  );
