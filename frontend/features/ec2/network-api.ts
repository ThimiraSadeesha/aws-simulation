"use client";

import { useQuery } from "@tanstack/react-query";
import { apiAll } from "@/lib/api/client";
import type { SecurityGroup, Subnet, Vpc } from "@/lib/types";

/** Small read-only lookups used by EC2 forms. Keys start with "vpc" so VPC mutations invalidate them. */
export function useVpcOptions() {
  return useQuery({
    queryKey: ["vpc", "vpcs", "options"],
    queryFn: ({ signal }) => apiAll<Vpc>("/vpc/vpcs", {}, signal),
    staleTime: 30_000,
  });
}

export function useSubnetOptions() {
  return useQuery({
    queryKey: ["vpc", "subnets", "options"],
    queryFn: ({ signal }) => apiAll<Subnet>("/vpc/subnets", {}, signal),
    staleTime: 30_000,
  });
}

export function useSecurityGroupOptions(vpcId?: string) {
  return useQuery({
    queryKey: ["vpc", "security-groups", "options", vpcId ?? "all"],
    queryFn: ({ signal }) => apiAll<SecurityGroup>("/vpc/security-groups", vpcId ? { vpc_id: vpcId } : {}, signal),
    staleTime: 30_000,
  });
}
