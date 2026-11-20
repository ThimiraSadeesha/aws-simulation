"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { formatDate, tagsToString } from "@/lib/format";
import type { Vpc } from "@/lib/types";
import { useDeleteVpc, useVpcs } from "@/features/vpc/api";
import { CreateVpcDialog } from "@/features/vpc/components/create-vpc-dialog";

export default function VpcsPage() {
  const list = useListParams({ sort: "created_at", order: "desc" });
  const q = useVpcs(list.params);
  const del = useDeleteVpc();
  const router = useRouter();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<Vpc[]>([]);

  const columns: Column<Vpc>[] = [
    {
      id: "name",
      header: "Name",
      sortable: true,
      cell: (v) => (
        <Link href={`/vpc/vpcs/${v.vpc_id}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {v.name || "(unnamed)"}
        </Link>
      ),
    },
    { id: "vpc_id", header: "VPC ID", sortable: true, cell: (v) => <Mono copy>{v.vpc_id}</Mono> },
    { id: "state", header: "State", sortable: true, cell: (v) => <StatusBadge status={v.state} /> },
    { id: "cidr_block", header: "IPv4 CIDR", sortable: true, cell: (v) => <span className="font-mono text-xs">{v.cidr_block}</span> },
    { id: "is_default", header: "Default", cell: (v) => (v.is_default ? "Yes" : "No") },
    { id: "dns", header: "DNS hostnames", cell: (v) => (v.enable_dns_hostnames ? "Enabled" : "Disabled") },
    { id: "tags", header: "Tags", cell: (v) => <span className="text-xs text-muted-foreground">{tagsToString(v.tags) || "—"}</span> },
    { id: "created_at", header: "Created", sortable: true, cell: (v) => <span className="text-xs">{formatDate(v.created_at)}</span> },
  ];

  return (
    <div>
      <PageHeader
        title="Your VPCs"
        description="Isolated virtual networks. Deleting a VPC fails while subnets, gateways or instances still exist."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> Create VPC
          </Button>
        }
      />
      <ResourceTable
        label="VPCs"
        columns={columns}
        getRowId={(v) => v.vpc_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        selectable
        onRowClick={(v) => router.push(`/vpc/vpcs/${v.vpc_id}`)}
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        emptyTitle="No VPCs yet"
        emptyDescription="A default VPC (172.31.0.0/16) is created automatically the first time you launch an instance without a subnet."
        emptyAction={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create VPC
          </Button>
        }
      />
      <CreateVpcDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length} VPC${toDelete.length === 1 ? "" : "s"}?`}
        description={
          <span>
            This deletes <span className="font-mono">{toDelete.map((v) => v.vpc_id).join(", ")}</span> with its main route table, default ACL and
            default security group. It fails with DependencyViolation while subnets, internet gateways or instances exist.
          </span>
        }
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const v of toDelete) await del.mutateAsync(v.vpc_id);
        }}
      />
    </div>
  );
}
