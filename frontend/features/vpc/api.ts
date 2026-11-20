"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList } from "@/lib/api/client";
import type {
  DnsResolveResponse,
  InternetGateway,
  ListParams,
  NatGateway,
  NetworkAcl,
  NetworkAclEntry,
  NetworkInterface,
  NewSecurityGroupRule,
  ReachabilityRequest,
  ReachabilityResponse,
  RouteTable,
  SecurityGroup,
  Subnet,
  Tags,
  Topology,
  Vpc,
} from "@/lib/types";

const enc = encodeURIComponent;
const POLL_MS = 2000;

export const vpcKeys = {
  all: ["vpc"] as const,
  list: (kind: string, p?: ListParams) => ["vpc", kind, "list", p ?? {}] as const,
  all_: (kind: string, q?: Record<string, string>) => ["vpc", kind, "all", q ?? {}] as const,
  one: (kind: string, id: string) => ["vpc", kind, "one", id] as const,
  topology: (id: string) => ["vpc", "vpcs", "topology", id] as const,
};

function useInvalidateVpc() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: vpcKeys.all });
    void qc.invalidateQueries({ queryKey: ["dashboard"] });
  };
}

// ---------- generic list / get ----------
function useList<T>(kind: string, path: string, params: ListParams, refetch?: (items: T[]) => boolean) {
  return useQuery({
    queryKey: vpcKeys.list(kind, params),
    queryFn: ({ signal }) => apiList<T>(path, params, signal),
    placeholderData: keepPreviousData,
    refetchInterval: refetch ? (q) => (q.state.data && refetch(q.state.data.items) ? POLL_MS : false) : undefined,
  });
}

function useAllOf<T>(kind: string, path: string, query: Record<string, string> = {}, enabled = true) {
  return useQuery({
    queryKey: vpcKeys.all_(kind, query),
    queryFn: ({ signal }) => apiAll<T>(path, query, signal),
    staleTime: 10_000,
    enabled,
  });
}

function useOne<T>(kind: string, path: string, id: string) {
  return useQuery({
    queryKey: vpcKeys.one(kind, id),
    queryFn: ({ signal }) => api<T>(`${path}/${enc(id)}`, { signal }),
    enabled: !!id,
  });
}

// VPCs
export const useVpcs = (p: ListParams) => useList<Vpc>("vpcs", "/vpc/vpcs", p);
export const useAllVpcs = () => useAllOf<Vpc>("vpcs", "/vpc/vpcs");
export const useVpc = (id: string) => useOne<Vpc>("vpcs", "/vpc/vpcs", id);
export function useTopology(id: string) {
  return useQuery({
    queryKey: vpcKeys.topology(id),
    queryFn: ({ signal }) => api<Topology>(`/vpc/vpcs/${enc(id)}/topology`, { signal }),
    enabled: !!id,
    refetchInterval: (q) =>
      q.state.data?.instances.some((i) => ["pending", "stopping", "shutting-down", "rebooting"].includes(i.state)) ||
      q.state.data?.nat_gateways.some((n) => n.state === "pending" || n.state === "deleting")
        ? POLL_MS
        : false,
  });
}

// Subnets
export const useSubnets = (p: ListParams) => useList<Subnet>("subnets", "/vpc/subnets", p);
export const useAllSubnets = (vpcId?: string) =>
  useAllOf<Subnet>("subnets", "/vpc/subnets", vpcId ? { vpc_id: vpcId } : {});
export const useSubnet = (id: string) => useOne<Subnet>("subnets", "/vpc/subnets", id);

// Route tables
export const useRouteTables = (p: ListParams) => useList<RouteTable>("route-tables", "/vpc/route-tables", p);
export const useRouteTable = (id: string) => useOne<RouteTable>("route-tables", "/vpc/route-tables", id);

// IGW / NAT
export const useInternetGateways = (p: ListParams) =>
  useList<InternetGateway>("internet-gateways", "/vpc/internet-gateways", p);
export const useAllInternetGateways = () => useAllOf<InternetGateway>("internet-gateways", "/vpc/internet-gateways");
export const useNatGateways = (p: ListParams) =>
  useList<NatGateway>("nat-gateways", "/vpc/nat-gateways", p, (items) =>
    items.some((n) => n.state === "pending" || n.state === "deleting"),
  );
export const useAllNatGateways = () => useAllOf<NatGateway>("nat-gateways", "/vpc/nat-gateways");

// Security groups
export const useSecurityGroups = (p: ListParams) => useList<SecurityGroup>("security-groups", "/vpc/security-groups", p);
export const useAllSecurityGroups = (vpcId?: string) =>
  useAllOf<SecurityGroup>("security-groups", "/vpc/security-groups", vpcId ? { vpc_id: vpcId } : {});
export const useSecurityGroup = (id: string) => useOne<SecurityGroup>("security-groups", "/vpc/security-groups", id);

// NACLs
export const useNetworkAcls = (p: ListParams) => useList<NetworkAcl>("network-acls", "/vpc/network-acls", p);
export const useNetworkAcl = (id: string) => useOne<NetworkAcl>("network-acls", "/vpc/network-acls", id);

// ENIs
export const useNetworkInterfaces = (p: ListParams) =>
  useList<NetworkInterface>("network-interfaces", "/vpc/network-interfaces", p);

// ---------- mutations ----------
type Method = "POST" | "PUT" | "PATCH" | "DELETE";

/** Small factory: a mutation calling `path(vars)` and invalidating every "vpc" query. */
export function useVpcMutation<V, R = unknown>(
  method: Method,
  path: (v: V) => string,
  body?: (v: V) => unknown,
  opts: { silent?: boolean; query?: (v: V) => Record<string, string | number | boolean> } = {},
) {
  const invalidate = useInvalidateVpc();
  return useMutation({
    mutationFn: (v: V) => api<R>(path(v), { method, body: body ? body(v) : undefined, query: opts.query?.(v) }),
    meta: opts.silent ? { silent: true } : undefined,
    onSuccess: invalidate,
  });
}

export const useCreateVpc = () =>
  useVpcMutation<{ cidr_block: string; name?: string; tags?: Tags }, Vpc>("POST", () => "/vpc/vpcs", (v) => v, {
    silent: true,
  });
export const usePatchVpc = (id: string) =>
  useVpcMutation<Partial<Pick<Vpc, "name" | "enable_dns_support" | "enable_dns_hostnames">>, Vpc>(
    "PATCH",
    () => `/vpc/vpcs/${enc(id)}`,
    (v) => v,
  );
export const useDeleteVpc = () => useVpcMutation<string>("DELETE", (id) => `/vpc/vpcs/${enc(id)}`);

export const useCreateSubnet = () =>
  useVpcMutation<{ vpc_id: string; cidr_block: string; name?: string }, Subnet>(
    "POST",
    () => "/vpc/subnets",
    (v) => v,
    { silent: true },
  );
export const usePatchSubnet = () =>
  useVpcMutation<{ id: string; name?: string; map_public_ip_on_launch?: boolean }, Subnet>(
    "PATCH",
    (v) => `/vpc/subnets/${enc(v.id)}`,
    (v) => ({ name: v.name, map_public_ip_on_launch: v.map_public_ip_on_launch }),
  );
export const useDeleteSubnet = () => useVpcMutation<string>("DELETE", (id) => `/vpc/subnets/${enc(id)}`);

export const useCreateRouteTable = () =>
  useVpcMutation<{ vpc_id: string; name?: string }, RouteTable>("POST", () => "/vpc/route-tables", (v) => v, {
    silent: true,
  });
export const useDeleteRouteTable = () => useVpcMutation<string>("DELETE", (id) => `/vpc/route-tables/${enc(id)}`);
export const useAddRoute = (rtb: string) =>
  useVpcMutation<{ destination_cidr: string; target_id: string }>(
    "POST",
    () => `/vpc/route-tables/${enc(rtb)}/routes`,
    (v) => v,
    { silent: true },
  );
export const useDeleteRoute = (rtb: string) =>
  useVpcMutation<string>("DELETE", () => `/vpc/route-tables/${enc(rtb)}/routes`, undefined, {
    query: (cidr) => ({ destination_cidr: cidr }),
  });
export const useAssociateRouteTable = (rtb: string) =>
  useVpcMutation<string>("POST", () => `/vpc/route-tables/${enc(rtb)}/associations`, (subnet_id) => ({ subnet_id }));

export const useCreateIgw = () =>
  useVpcMutation<{ name?: string }, InternetGateway>("POST", () => "/vpc/internet-gateways", (v) => v, { silent: true });
export const useDeleteIgw = () => useVpcMutation<string>("DELETE", (id) => `/vpc/internet-gateways/${enc(id)}`);
export const useAttachIgw = () =>
  useVpcMutation<{ id: string; vpc_id: string }>(
    "POST",
    (v) => `/vpc/internet-gateways/${enc(v.id)}/attach`,
    (v) => ({ vpc_id: v.vpc_id }),
  );
export const useDetachIgw = () =>
  useVpcMutation<string>("POST", (id) => `/vpc/internet-gateways/${enc(id)}/detach`);

export const useCreateNat = () =>
  useVpcMutation<{ subnet_id: string; name?: string }, NatGateway>("POST", () => "/vpc/nat-gateways", (v) => v, {
    silent: true,
  });
export const useDeleteNat = () => useVpcMutation<string>("DELETE", (id) => `/vpc/nat-gateways/${enc(id)}`);

export const useCreateSecurityGroup = () =>
  useVpcMutation<{ group_name: string; description: string; vpc_id: string }, SecurityGroup>(
    "POST",
    () => "/vpc/security-groups",
    (v) => v,
    { silent: true },
  );
export const useDeleteSecurityGroup = () =>
  useVpcMutation<string>("DELETE", (id) => `/vpc/security-groups/${enc(id)}`);
export const useAddSgRule = (sg: string) =>
  useVpcMutation<NewSecurityGroupRule>("POST", () => `/vpc/security-groups/${enc(sg)}/rules`, (v) => v, {
    silent: true,
  });
export const useDeleteSgRule = (sg: string) =>
  useVpcMutation<string>("DELETE", (ruleId) => `/vpc/security-groups/${enc(sg)}/rules/${enc(ruleId)}`);

export const useCreateNacl = () =>
  useVpcMutation<{ vpc_id: string; name?: string }, NetworkAcl>("POST", () => "/vpc/network-acls", (v) => v, {
    silent: true,
  });
export const useDeleteNacl = () => useVpcMutation<string>("DELETE", (id) => `/vpc/network-acls/${enc(id)}`);
export const useAddNaclEntry = (acl: string) =>
  useVpcMutation<NetworkAclEntry>("POST", () => `/vpc/network-acls/${enc(acl)}/entries`, (v) => v, { silent: true });
export const useDeleteNaclEntry = (acl: string) =>
  useVpcMutation<{ rule_number: number; egress: boolean }>(
    "DELETE",
    () => `/vpc/network-acls/${enc(acl)}/entries`,
    undefined,
    { query: (v) => ({ rule_number: v.rule_number, egress: v.egress }) },
  );
export const useAssociateNacl = (acl: string) =>
  useVpcMutation<string>("POST", () => `/vpc/network-acls/${enc(acl)}/associations`, (subnet_id) => ({ subnet_id }));

export const useCreateEni = () =>
  useVpcMutation<{ subnet_id: string; description?: string; security_group_ids?: string[] }, NetworkInterface>(
    "POST",
    () => "/vpc/network-interfaces",
    (v) => v,
    { silent: true },
  );
export const useDeleteEni = () => useVpcMutation<string>("DELETE", (id) => `/vpc/network-interfaces/${enc(id)}`);

// ---------- tools ----------
export function useDnsResolve() {
  return useMutation({
    mutationFn: (v: { name: string; vpc_id?: string }) =>
      api<DnsResolveResponse>("/vpc/dns/resolve", { query: { name: v.name, vpc_id: v.vpc_id } }),
    meta: { silent: true },
  });
}

export function useReachability() {
  return useMutation({
    mutationFn: (body: ReachabilityRequest) =>
      api<ReachabilityResponse>("/vpc/reachability", { method: "POST", body }),
    meta: { silent: true },
  });
}
