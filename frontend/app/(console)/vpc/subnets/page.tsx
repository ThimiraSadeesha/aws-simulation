"use client";

import { useState } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import type { Subnet } from "@/lib/types";
import { useAllVpcs, useDeleteSubnet, useSubnets } from "@/features/vpc/api";
import { IdLink } from "@/features/vpc/components/common";
import { CreateSubnetDialog, EditSubnetDialog } from "@/features/vpc/components/subnet-dialogs";

export default function SubnetsPage() {
  const list = useListParams();
  const q = useSubnets(list.params);
  const vpcs = useAllVpcs();
  const del = useDeleteSubnet();
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<Subnet | null>(null);
  const [toDelete, setToDelete] = useState<Subnet[]>([]);

  const columns: Column<Subnet>[] = [
    { id: "name", header: "Name", sortable: true, cell: (s) => s.name || <span className="text-muted-foreground">—</span> },
    { id: "subnet_id", header: "Subnet ID", sortable: true, cell: (s) => <Mono copy>{s.subnet_id}</Mono> },
    { id: "vpc_id", header: "VPC", sortable: true, cell: (s) => <IdLink id={s.vpc_id} /> },
    { id: "cidr_block", header: "IPv4 CIDR", sortable: true, cell: (s) => <span className="font-mono text-xs">{s.cidr_block}</span> },
    {
      id: "is_public",
      header: "Type",
      cell: (s) => <StatusBadge status={s.is_public ? "available" : "stopped"} label={s.is_public ? "public" : "private"} />,
    },
    { id: "availability_zone", header: "AZ", cell: (s) => s.availability_zone },
    { id: "available_ip_count", header: "Available IPs", sortable: true, cell: (s) => s.available_ip_count },
    { id: "map_public_ip_on_launch", header: "Auto-assign public IP", cell: (s) => (s.map_public_ip_on_launch ? "Yes" : "No") },
    { id: "route_table_id", header: "Route table", cell: (s) => <IdLink id={s.route_table_id} /> },
    { id: "network_acl_id", header: "Network ACL", cell: (s) => <IdLink id={s.network_acl_id} /> },
    {
      id: "actions",
      header: "",
      cell: (s) => (
        <Button size="icon-xs" variant="ghost" aria-label={`Edit ${s.subnet_id}`} onClick={() => setEditing(s)}>
          <Pencil />
        </Button>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="Subnets"
        description="A subnet is public when its route table has 0.0.0.0/0 → an internet gateway."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> Create subnet
          </Button>
        }
      />
      <ResourceTable
        label="Subnets"
        columns={columns}
        getRowId={(s) => s.subnet_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        filters={[
          {
            key: "vpc_id",
            label: "VPC",
            options: (vpcs.data ?? []).map((v) => ({ value: v.vpc_id, label: `${v.vpc_id}${v.name ? ` (${v.name})` : ""}` })),
          },
        ]}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        emptyTitle="No subnets"
        emptyAction={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create subnet
          </Button>
        }
      />
      <CreateSubnetDialog key={list.state.filters.vpc_id ?? "all"} open={createOpen} onOpenChange={setCreateOpen} defaultVpcId={list.state.filters.vpc_id ?? ""} />
      <EditSubnetDialog subnet={editing} onClose={() => setEditing(null)} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length} subnet(s)?`}
        description={<span className="font-mono">{toDelete.map((s) => s.subnet_id).join(", ")}</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const s of toDelete) await del.mutateAsync(s.subnet_id);
        }}
      />
    </div>
  );
}
