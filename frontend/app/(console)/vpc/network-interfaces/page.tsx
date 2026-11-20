"use client";

import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormField } from "@/components/console/form-field";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import type { NetworkInterface } from "@/lib/types";
import { useAllSubnets, useCreateEni, useDeleteEni, useNetworkInterfaces } from "@/features/vpc/api";
import { FormDialog, IdLink, SecurityGroupMultiSelect, SubnetSelect } from "@/features/vpc/components/common";

export default function NetworkInterfacesPage() {
  const list = useListParams();
  const q = useNetworkInterfaces(list.params);
  const create = useCreateEni();
  const del = useDeleteEni();
  const subnets = useAllSubnets();
  const [open, setOpen] = useState(false);
  const [subnetId, setSubnetId] = useState("");
  const [description, setDescription] = useState("");
  const [sgs, setSgs] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<NetworkInterface[]>([]);
  const subnetVpc = subnets.data?.find((s) => s.subnet_id === subnetId)?.vpc_id;

  const columns: Column<NetworkInterface>[] = [
    { id: "network_interface_id", header: "Interface ID", sortable: true, cell: (n) => <Mono copy>{n.network_interface_id}</Mono> },
    { id: "status", header: "Status", sortable: true, cell: (n) => <StatusBadge status={n.status} /> },
    { id: "subnet_id", header: "Subnet", cell: (n) => <span className="font-mono text-xs">{n.subnet_id}</span> },
    { id: "vpc_id", header: "VPC", cell: (n) => <IdLink id={n.vpc_id} /> },
    { id: "private_ip", header: "Private IP", cell: (n) => <span className="font-mono text-xs">{n.private_ip || "—"}</span> },
    { id: "public_ip", header: "Public IP", cell: (n) => <span className="font-mono text-xs">{n.public_ip || "—"}</span> },
    { id: "attachment", header: "Attached to", cell: (n) => (n.attachment ? <span><IdLink id={n.attachment.instance_id} /> <span className="text-xs text-muted-foreground">(eth{n.attachment.device_index})</span></span> : "—") },
    { id: "security_group_ids", header: "Security groups", cell: (n) => <span className="font-mono text-xs">{n.security_group_ids?.join(", ") || "—"}</span> },
    { id: "description", header: "Description", cell: (n) => <span className="text-xs text-muted-foreground">{n.description || "—"}</span> },
  ];

  return (
    <div>
      <PageHeader
        title="Network interfaces"
        description="Every instance gets a primary interface automatically. Interfaces that are in use cannot be deleted."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Create network interface
          </Button>
        }
      />
      <ResourceTable
        label="Network interfaces"
        columns={columns}
        getRowId={(n) => n.network_interface_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        filters={[
          {
            key: "status",
            label: "Status",
            options: [
              { value: "in-use", label: "in-use" },
              { value: "available", label: "available" },
            ],
          },
        ]}
        selectable
        bulkActions={[
          { label: "Delete", icon: <Trash2 />, destructive: true, disabled: (rows) => rows.some((r) => r.status === "in-use"), onClick: setToDelete },
        ]}
        emptyTitle="No network interfaces"
      />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Create network interface"
        onSubmit={() => {
          if (!subnetId) {
            setErr("Choose a subnet.");
            return;
          }
          setErr(null);
          create.mutate(
            { subnet_id: subnetId, description: description.trim() || undefined, security_group_ids: sgs.length ? sgs : undefined },
            {
              onSuccess: (n) => {
                toast.success(`${n.network_interface_id} created`);
                setOpen(false);
                setDescription("");
                setSgs([]);
              },
              onError: (e) => setErr(errorMessage(e)),
            },
          );
        }}
        pending={create.isPending}
        error={err}
      >
        <FormField label="Subnet" required>
          {(p) => (
            <SubnetSelect
              {...p}
              value={subnetId}
              onChange={(v) => {
                setSubnetId(v);
                setSgs([]);
              }}
            />
          )}
        </FormField>
        <FormField label="Description">{(p) => <Input {...p} value={description} onChange={(e) => setDescription(e.target.value)} />}</FormField>
        {subnetVpc && (
          <FormField label="Security groups">{(p) => <SecurityGroupMultiSelect id={p.id} value={sgs} onChange={setSgs} vpcId={subnetVpc} />}</FormField>
        )}
      </FormDialog>
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title="Delete network interface(s)?"
        description={<span className="font-mono">{toDelete.map((n) => n.network_interface_id).join(", ")}</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const n of toDelete) await del.mutateAsync(n.network_interface_id);
        }}
      />
    </div>
  );
}
