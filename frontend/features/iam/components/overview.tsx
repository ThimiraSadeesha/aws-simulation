"use client";

import Link from "next/link";
import { PageHeader } from "@/components/console/page-header";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { Mono } from "@/components/console/copy-button";
import { ErrorState, LoadingState } from "@/components/console/states";
import { useCallerIdentity, useIamGroups, useIamPolicies, useIamRoles, useIamUsers } from "../api";

const ONE = { page: 1, page_size: 1 };

function Count({ href, label, q }: { href: string; label: string; q: { data?: { total: number }; isLoading: boolean; isError: boolean } }) {
  return (
    <Link href={href} className="rounded-lg border bg-card p-4 transition-colors hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:outline-none">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{q.isLoading ? "…" : q.isError ? "—" : (q.data?.total ?? 0)}</div>
    </Link>
  );
}

export function IamOverview() {
  const users = useIamUsers(ONE);
  const groups = useIamGroups(ONE);
  const roles = useIamRoles(ONE);
  const policies = useIamPolicies(ONE);
  const identity = useCallerIdentity();
  return (
    <div className="grid gap-4">
      <PageHeader
        title="Identity and Access Management"
        description="Manage IAM users, groups, roles and policies. Requests made with access keys are authorized by the policy evaluation engine (explicit Deny > Allow > implicit deny)."
      />
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Count href="/iam/users" label="Users" q={users} />
        <Count href="/iam/groups" label="Groups" q={groups} />
        <Count href="/iam/roles" label="Roles" q={roles} />
        <Count href="/iam/policies" label="Policies" q={policies} />
      </div>
      <Section title="Caller identity (GET /sts/caller-identity)">
        {identity.isLoading ? (
          <LoadingState />
        ) : identity.isError ? (
          <ErrorState error={identity.error} onRetry={() => void identity.refetch()} compact />
        ) : (
          <DetailGrid
            items={[
              { label: "Account", value: <Mono copy>{identity.data?.account}</Mono> },
              { label: "ARN", value: <Mono copy>{identity.data?.arn}</Mono> },
              { label: "User ID", value: <Mono>{identity.data?.user_id}</Mono> },
            ]}
          />
        )}
      </Section>
    </div>
  );
}
