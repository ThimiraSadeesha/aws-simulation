"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormField } from "@/components/console/form-field";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import type { RouteTable } from "@/lib/types";
import { useAllVpcs, useCreateRouteTable, useDeleteRouteTable, useRouteTables } from "@/features/vpc/api";
import { FormDialog, IdLink, VpcSelect } from "@/features/vpc/components/common";

export default function RouteTablesPage() {
  const list = useListParams();
  const q = useRouteTables(list.params);
  const vpcs = useAllVpcs();
  const del = useDeleteRouteTable();
  const create = useCreateRouteTable();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [vpcId, setVpcId] = useState("");
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<RouteTable[]>([]);

  const columns: Column<RouteTable>[] = [
    {
      id: "route_table_id",
      header: "Route table ID",
      sortable: true,
      cell: (r) => (
        <Link href={`/vpc/route-tables/${r.route_table_id}`} className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {r.route_table_id}
        </Link>
      ),
    },
    { id: "name", header: "Name", sortable: true, cell: (r) => r.name || "—" },
    { id: "vpc_id", header: "VPC", sortable: true, cell: (r) => <IdLink id={r.vpc_id} /> },
    { id: "is_main", header: "Main", cell: (r) => (r.is_main ? "Yes" : "No") },
    { id: "routes", header: "Routes", cell: (r) => r.routes.length },
    { id: "subnet_ids", header: "Explicit associations", cell: (r) => (r.subnet_ids.length ? r.subnet_ids.join(", ") : "—") },
  ];

  const submit = () => {
    if (!vpcId) {
      setErr("Choose a VPC.");
      return;
    }
    setErr(null);
    create.mutate(
      { vpc_id: vpcId, name: name.trim() || undefined },
      {
        onSuccess: (rt) => {
          toast.success(`Route table ${rt.route_table_id} created`);
          setOpen(false);
          setName("");
          router.push(`/vpc/route-tables/${rt.route_table_id}`);
        },
        onError: (e) => setErr(errorMessage(e)),
      },
    );
  };

  return (
    <div>
      <PageHeader
        title="Route tables"
        description="Routes use longest-prefix match. Every VPC has a main route table with a local route."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Create route table
          </Button>
        }
      />
      <ResourceTable
        label="Route tables"
        columns={columns}
        getRowId={(r) => r.route_table_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        onRowClick={(r) => router.push(`/vpc/route-tables/${r.route_table_id}`)}
        filters={[{ key: "vpc_id", label: "VPC", options: (vpcs.data ?? []).map((v) => ({ value: v.vpc_id, label: v.vpc_id })) }]}
        selectable
        bulkActions={[
          { label: "Delete", icon: <Trash2 />, destructive: true, disabled: (rows) => rows.some((r) => r.is_main), onClick: setToDelete },
        ]}
        emptyTitle="No route tables"
      />
      <FormDialog open={open} onOpenChange={setOpen} title="Create route table" onSubmit={submit} pending={create.isPending} error={err}>
        <FormField label="VPC" required>
          {(p) => <VpcSelect {...p} value={vpcId} onChange={setVpcId} />}
        </FormField>
        <FormField label="Name">{(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} />}</FormField>
      </FormDialog>
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title="Delete route table(s)?"
        description={<span className="font-mono">{toDelete.map((r) => r.route_table_id).join(", ")}</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const r of toDelete) await del.mutateAsync(r.route_table_id);
        }}
      />
    </div>
  );
}
