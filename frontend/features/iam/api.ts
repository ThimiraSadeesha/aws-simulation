"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiAll, apiList } from "@/lib/api/client";
import type {
  AccessKey,
  AttachedPolicy,
  CreatedAccessKey,
  IamGroup,
  IamPolicy,
  IamRole,
  IamUser,
  ListParams,
  PolicyDocument,
  SimulateRequest,
  SimulateResponse,
  Tags,
} from "@/lib/types";
import type { AssumeRoleResponse, CallerIdentity, PrincipalKind } from "./types";

const enc = encodeURIComponent;

export const iamKeys = {
  all: ["iam"] as const,
  users: (p?: ListParams) => ["iam", "users", p ?? {}] as const,
  user: (n: string) => ["iam", "user", n] as const,
  accessKeys: (n: string) => ["iam", "user", n, "access-keys"] as const,
  groups: (p?: ListParams) => ["iam", "groups", p ?? {}] as const,
  group: (n: string) => ["iam", "group", n] as const,
  roles: (p?: ListParams) => ["iam", "roles", p ?? {}] as const,
  role: (n: string) => ["iam", "role", n] as const,
  policies: (p?: ListParams) => ["iam", "policies", p ?? {}] as const,
  policy: (n: string) => ["iam", "policy", n] as const,
  callerIdentity: ["iam", "caller-identity"] as const,
};

// ---------- normalisers (response shapes are loosely specified in docs/api.md) ----------
export function groupNames(groups: IamUser["groups"]): string[] {
  return (groups ?? []).map((g) => (typeof g === "string" ? g : g.group_name));
}
export function inlineNames(list: IamUser["inline_policies"]): string[] {
  return (list ?? []).map((p) => (typeof p === "string" ? p : p.policy_name));
}
export function attachedName(p: AttachedPolicy | string): string {
  return typeof p === "string" ? p : p.policy_name;
}
export function policyIsBuiltin(p: IamPolicy): boolean {
  if (p.is_builtin !== undefined) return p.is_builtin;
  if (p.builtin !== undefined) return p.builtin;
  // Built-in managed policies have no account id in their ARN (AWS-style `arn:…:iam::aws:policy/…`) or a CloudSim name.
  return /:iam::(aws|cloudsim)?:policy\//.test(p.arn ?? "") || p.policy_name === "AdministratorAccess" || p.policy_name === "ReadOnlyAccess";
}
export function groupMembers(g: IamGroup): string[] {
  const raw = (g.members ?? g.users ?? []) as unknown[];
  return raw.map((m) => (typeof m === "string" ? m : ((m as { user_name?: string }).user_name ?? String(m))));
}

// ---------- queries ----------
export function useIamUsers(params: ListParams) {
  return useQuery({
    queryKey: iamKeys.users(params),
    queryFn: ({ signal }) => apiList<IamUser>("/iam/users", params, signal),
    placeholderData: keepPreviousData,
  });
}
export function useAllIamUsers() {
  return useQuery({ queryKey: ["iam", "users", "all"], queryFn: ({ signal }) => apiAll<IamUser>("/iam/users", {}, signal) });
}
export function useIamUser(name: string) {
  return useQuery({ queryKey: iamKeys.user(name), queryFn: ({ signal }) => api<IamUser>(`/iam/users/${enc(name)}`, { signal }), enabled: !!name });
}
export function useAccessKeys(name: string) {
  return useQuery({
    queryKey: iamKeys.accessKeys(name),
    queryFn: ({ signal }) => apiAll<AccessKey>(`/iam/users/${enc(name)}/access-keys`, {}, signal),
    enabled: !!name,
  });
}
export function useIamGroups(params: ListParams) {
  return useQuery({
    queryKey: iamKeys.groups(params),
    queryFn: ({ signal }) => apiList<IamGroup>("/iam/groups", params, signal),
    placeholderData: keepPreviousData,
  });
}
export function useAllIamGroups() {
  return useQuery({ queryKey: ["iam", "groups", "all"], queryFn: ({ signal }) => apiAll<IamGroup>("/iam/groups", {}, signal) });
}
export function useIamGroup(name: string) {
  return useQuery({ queryKey: iamKeys.group(name), queryFn: ({ signal }) => api<IamGroup>(`/iam/groups/${enc(name)}`, { signal }), enabled: !!name });
}
export function useIamRoles(params: ListParams) {
  return useQuery({
    queryKey: iamKeys.roles(params),
    queryFn: ({ signal }) => apiList<IamRole>("/iam/roles", params, signal),
    placeholderData: keepPreviousData,
  });
}
export function useAllIamRoles() {
  return useQuery({ queryKey: ["iam", "roles", "all"], queryFn: ({ signal }) => apiAll<IamRole>("/iam/roles", {}, signal) });
}
export function useIamRole(name: string) {
  return useQuery({ queryKey: iamKeys.role(name), queryFn: ({ signal }) => api<IamRole>(`/iam/roles/${enc(name)}`, { signal }), enabled: !!name });
}
export function useIamPolicies(params: ListParams) {
  return useQuery({
    queryKey: iamKeys.policies(params),
    queryFn: ({ signal }) => apiList<IamPolicy>("/iam/policies", params, signal),
    placeholderData: keepPreviousData,
  });
}
export function useAllIamPolicies() {
  return useQuery({ queryKey: ["iam", "policies", "all"], queryFn: ({ signal }) => apiAll<IamPolicy>("/iam/policies", {}, signal) });
}
export function useIamPolicy(name: string) {
  return useQuery({ queryKey: iamKeys.policy(name), queryFn: ({ signal }) => api<IamPolicy>(`/iam/policies/${enc(name)}`, { signal }), enabled: !!name });
}
export function useCallerIdentity() {
  return useQuery({ queryKey: iamKeys.callerIdentity, queryFn: ({ signal }) => api<CallerIdentity>("/sts/caller-identity", { signal }), staleTime: 60_000 });
}

// ---------- mutations ----------
function useIamMutation<V, R = unknown>(fn: (v: V) => Promise<R>, silent = false) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    meta: silent ? { silent: true } : undefined,
    onSuccess: () => qc.invalidateQueries({ queryKey: iamKeys.all }),
  });
}

export const useCreateUser = () =>
  useIamMutation((b: { user_name: string; path?: string; tags?: Tags }) => api<IamUser>("/iam/users", { method: "POST", body: b }), true);
export const useDeleteUser = () => useIamMutation((name: string) => api<void>(`/iam/users/${enc(name)}`, { method: "DELETE" }));
export const useCreateAccessKey = () =>
  useIamMutation((user: string) => api<CreatedAccessKey>(`/iam/users/${enc(user)}/access-keys`, { method: "POST" }));
export const useUpdateAccessKey = () =>
  useIamMutation(({ user, keyId, status }: { user: string; keyId: string; status: "Active" | "Inactive" }) =>
    api<AccessKey>(`/iam/users/${enc(user)}/access-keys/${enc(keyId)}`, { method: "PATCH", body: { status } }),
  );
export const useDeleteAccessKey = () =>
  useIamMutation(({ user, keyId }: { user: string; keyId: string }) =>
    api<void>(`/iam/users/${enc(user)}/access-keys/${enc(keyId)}`, { method: "DELETE" }),
  );

export const useCreateGroup = () =>
  useIamMutation((b: { group_name: string; path?: string }) => api<IamGroup>("/iam/groups", { method: "POST", body: b }), true);
export const useDeleteGroup = () => useIamMutation((name: string) => api<void>(`/iam/groups/${enc(name)}`, { method: "DELETE" }));
export const useAddUserToGroup = () =>
  useIamMutation(({ group, user }: { group: string; user: string }) =>
    api<void>(`/iam/groups/${enc(group)}/members/${enc(user)}`, { method: "POST" }),
  );
export const useRemoveUserFromGroup = () =>
  useIamMutation(({ group, user }: { group: string; user: string }) =>
    api<void>(`/iam/groups/${enc(group)}/members/${enc(user)}`, { method: "DELETE" }),
  );

export const useCreateRole = () =>
  useIamMutation(
    (b: { role_name: string; assume_role_policy_document: PolicyDocument; description?: string; max_session_duration?: number }) =>
      api<IamRole>("/iam/roles", { method: "POST", body: b }),
    true,
  );
export const useDeleteRole = () => useIamMutation((name: string) => api<void>(`/iam/roles/${enc(name)}`, { method: "DELETE" }));

export const useCreatePolicy = () =>
  useIamMutation(
    (b: { policy_name: string; description?: string; policy_document: PolicyDocument }) =>
      api<IamPolicy>("/iam/policies", { method: "POST", body: b }),
    true,
  );
export const useUpdatePolicy = () =>
  useIamMutation(
    ({ name, document }: { name: string; document: PolicyDocument }) =>
      api<IamPolicy>(`/iam/policies/${enc(name)}`, { method: "PUT", body: { policy_document: document } }),
    true,
  );
export const useDeletePolicy = () => useIamMutation((name: string) => api<void>(`/iam/policies/${enc(name)}`, { method: "DELETE" }));

export const useAttachPolicy = () =>
  useIamMutation(({ kind, name, policy }: { kind: PrincipalKind; name: string; policy: string }) =>
    api<void>(`/iam/${kind}/${enc(name)}/attached-policies/${enc(policy)}`, { method: "POST" }),
  );
export const useDetachPolicy = () =>
  useIamMutation(({ kind, name, policy }: { kind: PrincipalKind; name: string; policy: string }) =>
    api<void>(`/iam/${kind}/${enc(name)}/attached-policies/${enc(policy)}`, { method: "DELETE" }),
  );
export const usePutInlinePolicy = () =>
  useIamMutation(
    ({ kind, name, policy, document }: { kind: PrincipalKind; name: string; policy: string; document: PolicyDocument }) =>
      api<void>(`/iam/${kind}/${enc(name)}/inline-policies/${enc(policy)}`, { method: "PUT", body: document }),
    true,
  );
export const useDeleteInlinePolicy = () =>
  useIamMutation(({ kind, name, policy }: { kind: PrincipalKind; name: string; policy: string }) =>
    api<void>(`/iam/${kind}/${enc(name)}/inline-policies/${enc(policy)}`, { method: "DELETE" }),
  );

export function useSimulate() {
  return useMutation({
    mutationFn: (b: SimulateRequest) => api<SimulateResponse>("/iam/simulate", { method: "POST", body: b }),
    meta: { silent: true },
  });
}
export function useAssumeRole() {
  return useMutation({
    mutationFn: (b: { role_arn: string; role_session_name: string; duration_seconds?: number }) =>
      api<AssumeRoleResponse>("/sts/assume-role", { method: "POST", body: b }),
    meta: { silent: true },
  });
}
