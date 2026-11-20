"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/console/page-header";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { ErrorState, LoadingState } from "@/components/console/states";
import type { SecurityGroupRule } from "@/lib/types";
import { useDeleteSgRule, useSecurityGroup } from "@/features/vpc/api";
import { IdLink, portRange, protocolLabel } from "@/features/vpc/components/common";
import { SgRuleForm } from "@/features/vpc/components/sg-rule-form";

export default function SecurityGroupDetailPage() {
  const { id } = useParams<{ id: string }>();
  const q = useSecurityGroup(id);
  const delRule = useDeleteSgRule(id);
  const [toDelete, setToDelete] = useState<SecurityGroupRule | null>(null);

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const sg = q.data;

  const cols = (dir: "Source" | "Destination"): Column<SecurityGroupRule>[] => [
    { id: "protocol", header: "Protocol", sortable: true, cell: (r) => protocolLabel(r.protocol) },
    { id: "from_port", header: "Port range", sortable: true, cell: (r) => portRange(r.protocol, r.from_port, r.to_port) },
    {
      id: "cidr",
      header: dir,
      cell: (r) => <span className="font-mono text-xs">{r.source_security_group_id || r.cidr || "—"}</span>,
    },
    { id: "description", header: "Description", cell: (r) => <span className="text-xs text-muted-foreground">{r.description || "—"}</span> },
    { id: "rule_id", header: "Rule ID", cell: (r) => <span className="font-mono text-xs text-muted-foreground">{r.rule_id}</span> },
    {
      id: "actions",
      header: "",
      cell: (r) => (
        <Button size="icon-xs" variant="ghost" aria-label={`Delete rule ${r.rule_id}`} onClick={() => setToDelete(r)}>
          <Trash2 />
        </Button>
      ),
    },
  ];

  return (
    <div className="grid gap-4">
      <PageHeader
        breadcrumbs={[{ label: "VPC", href: "/vpc" }, { label: "Security groups", href: "/vpc/security-groups" }, { label: sg.group_id }]}
        title={sg.group_name}
        description={sg.description}
      />
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "Group ID", value: <Mono copy>{sg.group_id}</Mono> },
            { label: "Group name", value: sg.group_name },
            { label: "VPC", value: <IdLink id={sg.vpc_id} /> },
          ]}
        />
      </Section>
      <Section title={`Inbound rules (${sg.ingress?.length ?? 0})`}>
        <ResourceTable label="Inbound rules" columns={cols("Source")} getRowId={(r) => r.rule_id} data={sg.ingress ?? []} hideSearch globalSearch={false} emptyTitle="No inbound rules — all inbound traffic is denied" />
      </Section>
      <Section title={`Outbound rules (${sg.egress?.length ?? 0})`}>
        <ResourceTable label="Outbound rules" columns={cols("Destination")} getRowId={(r) => r.rule_id} data={sg.egress ?? []} hideSearch globalSearch={false} emptyTitle="No outbound rules — all outbound traffic is denied" />
      </Section>
      <Section title="Edit rules">
        <SgRuleForm groupId={sg.group_id} vpcId={sg.vpc_id} />
      </Section>
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Delete rule?"
        description={
          toDelete && (
            <span>
              {protocolLabel(toDelete.protocol)} {portRange(toDelete.protocol, toDelete.from_port, toDelete.to_port)} ·{" "}
              <span className="font-mono">{toDelete.source_security_group_id || toDelete.cidr}</span>
            </span>
          )
        }
        destructive
        confirmLabel="Delete rule"
        onConfirm={() => delRule.mutateAsync(toDelete!.rule_id)}
      />
    </div>
  );
}
