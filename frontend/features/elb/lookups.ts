"use client";

import { useQuery } from "@tanstack/react-query";
import { apiAll } from "@/lib/api/client";
import type { Instance, SecurityGroup, Subnet, Vpc } from "@/lib/types";

/** Lookup lists for select inputs. Keys are rooted at the owning service so its invalidations refresh them. */
export function useVpcOptions() {
  return useQuery({ queryKey: ["vpc", "vpcs", "all"], queryFn: ({ signal }) => apiAll<Vpc>("/vpc/vpcs", {}, signal), staleTime: 30_000 });
}
export function useSubnetOptions(vpcId?: string) {
  return useQuery({
    queryKey: ["vpc", "subnets", "all", vpcId ?? ""],
    queryFn: ({ signal }) => apiAll<Subnet>("/vpc/subnets", vpcId ? { vpc_id: vpcId } : {}, signal),
    staleTime: 30_000,
  });
}
export function useSecurityGroupOptions(vpcId?: string) {
  return useQuery({
    queryKey: ["vpc", "security-groups", "all", vpcId ?? ""],
    queryFn: ({ signal }) => apiAll<SecurityGroup>("/vpc/security-groups", vpcId ? { vpc_id: vpcId } : {}, signal),
    staleTime: 30_000,
  });
}
export function useInstanceOptions(query: Record<string, string> = {}) {
  return useQuery({
    queryKey: ["ec2", "instances", "all", query],
    queryFn: ({ signal }) => apiAll<Instance>("/ec2/instances", query, signal),
    staleTime: 10_000,
  });
}
