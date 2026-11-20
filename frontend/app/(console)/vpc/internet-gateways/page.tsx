"use client";

import { useState } from "react";
import { Link2, Plus, Trash2, Unlink } from "lucide-react";
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
import type { InternetGateway } from "@/lib/types";
import { useAttachIgw, useCreateIgw, useDeleteIgw, useDetachIgw, useInternetGateways } from "@/features/vpc/api";
import { FormDialog, IdLink, VpcSelect } from "@/features/vpc/components/common";

export default function InternetGatewaysPage() {
  const list = useListParams();
  const q = useInternetGateways(list.params);
  const create = useCreateIgw();
  const del = useDeleteIgw();
  const attach = useAttachIgw();
  const detach = useDetachIgw();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [createErr, setCreateErr] = useState<string | null>(null);
  const [attaching, setAttaching] = useState<InternetGateway | null>(null);
  const [vpcId, setVpcId] = useState("");
  const [detaching, setDetaching] = useState<InternetGateway | null>(null);
  const [toDelete, setToDelete] = useState<InternetGateway[]>([]);

  const columns: Column<InternetGateway>[] = [
    { id: "name", header: "Name", sortable: true, cell: (g) => g.name || "—" },
    { id: "internet_gateway_id", header: "Internet gateway ID", sortable: true, cell: (g) => <Mono copy>{g.internet_gateway_id}</Mono> },
    { id: "state", header: "State", sortable: true, cell: (g) => <StatusBadge status={g.state} /> },
    { id: "vpc_id", header: "VPC", cell: (g) => <IdLink id={g.vpc_id} /> },
    {
      id: "actions",
      header: "Actions",
      cell: (g) =>
        g.state === "attached" ? (
          <Button size="xs" variant="outline" onClick={() => setDetaching(g)}>
            <Unlink /> Detach
          </Button>
        ) : (
          <Button
            size="xs"
            variant="outline"
            onClick={() => {
              setVpcId("");
              setAttaching(g);
            }}
          >
            <Link2 /> Attach to VPC
          </Button>
        ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="Internet gateways"
        description="Attach an internet gateway to a VPC and add a 0.0.0.0/0 route to it to make subnets public."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> Create internet gateway
          </Button>
        }
      />
      <ResourceTable
        label="Internet gateways"
        columns={columns}
        getRowId={(g) => g.internet_gateway_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        filters={[
          {
            key: "state",
            label: "State",
            options: [
              { value: "attached", label: "attached" },
              { value: "detached", label: "detached" },
            ],
          },
        ]}
        selectable
        bulkActions={[
          {
            label: "Delete",
            icon: <Trash2 />,
            destructive: true,
            disabled: (rows) => rows.some((r) => r.state === "attached"),
            onClick: setToDelete,
          },
        ]}
        emptyTitle="No internet gateways"
      />
      <FormDialog
        open={createOpen}
        onOpenChange={(o) => {
          setCreateOpen(o);
          setCreateErr(null);
        }}
        title="Create internet gateway"
        onSubmit={() =>
          create.mutate(
            { name: name.trim() || undefined },
            {
              onSuccess: (g) => {
                toast.success(`${g.internet_gateway_id} created (detached)`);
                setName("");
                setCreateOpen(false);
              },
              onError: (e) => setCreateErr(errorMessage(e)),
            },
          )
        }
        pending={create.isPending}
        error={createErr}
      >
        <FormField label="Name tag">{(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} />}</FormField>
      </FormDialog>
      <FormDialog
        open={!!attaching}
        onOpenChange={(o) => !o && setAttaching(null)}
        title={`Attach ${attaching?.internet_gateway_id ?? ""}`}
        submitLabel="Attach"
        onSubmit={() => {
          if (!attaching || !vpcId) return;
          attach.mutate(
            { id: attaching.internet_gateway_id, vpc_id: vpcId },
            {
              onSuccess: () => {
                toast.success(`Attached to ${vpcId}`);
                setAttaching(null);
              },
            },
          );
        }}
        pending={attach.isPending}
      >
        <FormField label="VPC" required>
          {(p) => <VpcSelect {...p} value={vpcId} onChange={setVpcId} />}
        </FormField>
      </FormDialog>
      <ConfirmDialog
        open={!!detaching}
        onOpenChange={(o) => !o && setDetaching(null)}
        title={`Detach ${detaching?.internet_gateway_id}?`}
        description={`Subnets routing 0.0.0.0/0 to this gateway in ${detaching?.vpc_id} will lose internet access (routes become blackhole).`}
        confirmLabel="Detach"
        destructive
        onConfirm={() => detach.mutateAsync(detaching!.internet_gateway_id)}
      />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title="Delete internet gateway(s)?"
        description={<span className="font-mono">{toDelete.map((g) => g.internet_gateway_id).join(", ")}</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const g of toDelete) await del.mutateAsync(g.internet_gateway_id);
        }}
      />
    </div>
  );
}
