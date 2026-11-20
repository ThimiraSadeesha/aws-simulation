"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, toList } from "@/lib/api/client";
import type { Lab, LabProgress, LabSummary, ValidateStepResponse } from "@/lib/types";

export const labKeys = {
  all: ["labs"] as const,
  list: ["labs", "list"] as const,
  detail: (id: string) => ["labs", "detail", id] as const,
};

export function useLabs() {
  return useQuery({
    queryKey: labKeys.list,
    queryFn: async ({ signal }) => toList<LabSummary>(await api<unknown>("/labs", { signal })).items,
  });
}

export function useLab(id: string) {
  return useQuery({
    queryKey: labKeys.detail(id),
    queryFn: ({ signal }) => api<Lab>(`/labs/${encodeURIComponent(id)}`, { signal }),
    enabled: !!id,
  });
}

export function useStartLab(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<LabProgress>(`/labs/${encodeURIComponent(id)}/start`, { method: "POST" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: labKeys.all });
      void qc.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
}

export function useValidateStep(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (index: number) =>
      api<ValidateStepResponse>(`/labs/${encodeURIComponent(id)}/steps/${index}/validate`, { method: "POST" }),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: labKeys.all });
      void qc.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
}

export function useResetLab(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<unknown>(`/labs/${encodeURIComponent(id)}/reset`, { method: "POST" }),
    onSuccess: () => {
      // Reset deletes lab-tagged resources across services.
      void qc.invalidateQueries();
    },
  });
}

/** Number of passed steps from a lab's progress. */
export function passedSteps(lab: Lab): number {
  return lab.progress?.steps?.filter((s) => s.status === "passed").length ?? 0;
}

/** Service console page to show next to the lab, derived from the lab's service label. */
export function consolePathForService(service: string | undefined): string {
  const s = (service ?? "").toLowerCase();
  const map: Record<string, string> = {
    ec2: "/ec2",
    s3: "/s3",
    vpc: "/vpc",
    iam: "/iam",
    elb: "/elb",
    "load balancing": "/elb",
    autoscaling: "/autoscaling",
    "auto scaling": "/autoscaling",
    cloudwatch: "/cloudwatch",
    sqs: "/sqs",
    sns: "/sns",
    dynamodb: "/dynamodb",
    route53: "/route53",
    "route 53": "/route53",
    secretsmanager: "/secrets",
    secrets: "/secrets",
  };
  return map[s] ?? "/dashboard";
}
