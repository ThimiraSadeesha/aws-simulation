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
import type { NatGateway } from "@/lib/types";
import { useCreateNat, useDeleteNat, useNatGateways } from "@/features/vpc/api";
import { FormDialog, IdLink, SubnetSelect } from "@/features/vpc/components/common";

const STATES = ["pending", "available", "deleting", "deleted"];

export default function NatGatewaysPage() {
  const list = useListParams();
  const q = useNatGateways(list.params);
  const create = useCreateNat();
  const del = useDeleteNat();
  const [open, setOpen] = useState(false);
  const [subnetId, setSubnetId] = useState("");
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<NatGateway[]>([]);

  const columns: Column<NatGateway>[] = [
    { id: "name", header: "Name", sortable: true, cell: (n) => n.name || "—" },
    { id: "nat_gateway_id", header: "NAT gateway ID", sortable: true, cell: (n) => <Mono copy>{n.nat_gateway_id}</Mono> },
    { id: "state", header: "State", sortable: true, cell: (n) => <StatusBadge status={n.state} /> },
    { id: "subnet_id", header: "Subnet", cell: (n) => <span className="font-mono text-xs">{n.subnet_id}</span> },
    { id: "vpc_id", header: "VPC", cell: (n) => <IdLink id={n.vpc_id} /> },
    { id: "public_ip", header: "Public IP", cell: (n) => <span className="font-mono text-xs">{n.public_ip || "—"}</span> },
    { id: "private_ip", header: "Private IP", cell: (n) => <span className="font-mono text-xs">{n.private_ip || "—"}</span> },
  ];

  const submit = () => {
    if (!subnetId) {
      setErr("Choose a subnet. Place NAT gateways in a public subnet so they can reach the internet.");
      return;
    }
    setErr(null);
    create.mutate(
      { subnet_id: subnetId, name: name.trim() || undefined },
      {
        onSuccess: (n) => {
          toast.success(`${n.nat_gateway_id} is ${n.state}`);
          setOpen(false);
          setName("");
          setSubnetId("");
        },
        onError: (e) => setErr(errorMessage(e)),
      },
    );
  };

  return (
    <div>
      <PageHeader
        title="NAT gateways"
        description="Outbound-only internet access for private subnets. The simulator engine drives the pending → available lifecycle."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Create NAT gateway
          </Button>
        }
      />
      <ResourceTable
        label="NAT gateways"
        columns={columns}
        getRowId={(n) => n.nat_gateway_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        filters={[{ key: "state", label: "State", options: STATES.map((s) => ({ value: s, label: s })) }]}
        selectable
        bulkActions={[
          {
            label: "Delete",
            icon: <Trash2 />,
            destructive: true,
            disabled: (rows) => rows.some((r) => r.state === "deleting" || r.state === "deleted"),
            onClick: setToDelete,
          },
        ]}
        emptyTitle="No NAT gateways"
      />
      <FormDialog open={open} onOpenChange={setOpen} title="Create NAT gateway" onSubmit={submit} pending={create.isPending} error={err}>
        <FormField label="Subnet" required hint="Choose a public subnet (route 0.0.0.0/0 → internet gateway).">
          {(p) => <SubnetSelect {...p} value={subnetId} onChange={setSubnetId} />}
        </FormField>
        <FormField label="Name tag">{(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} />}</FormField>
      </FormDialog>
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title="Delete NAT gateway(s)?"
        description={<span className="font-mono">{toDelete.map((n) => n.nat_gateway_id).join(", ")}</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const n of toDelete) await del.mutateAsync(n.nat_gateway_id);
        }}
      />
    </div>
  );
}
