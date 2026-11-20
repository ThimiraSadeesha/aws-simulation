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
import type { SecurityGroup } from "@/lib/types";
import { useAllVpcs, useCreateSecurityGroup, useDeleteSecurityGroup, useSecurityGroups } from "@/features/vpc/api";
import { FormDialog, IdLink, VpcSelect } from "@/features/vpc/components/common";

export default function SecurityGroupsPage() {
  const list = useListParams();
  const q = useSecurityGroups(list.params);
  const vpcs = useAllVpcs();
  const create = useCreateSecurityGroup();
  const del = useDeleteSecurityGroup();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ group_name: "", description: "", vpc_id: "" });
  const [touched, setTouched] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<SecurityGroup[]>([]);

  const nameErr = form.group_name.trim() ? (form.group_name.length > 255 ? "255 characters max." : null) : "Group name is required.";
  const descErr = form.description.length > 255 ? "255 characters max." : null;
  const vpcErr = form.vpc_id ? null : "Choose a VPC.";

  const columns: Column<SecurityGroup>[] = [
    {
      id: "group_id",
      header: "Group ID",
      sortable: true,
      cell: (g) => (
        <Link href={`/vpc/security-groups/${g.group_id}`} className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {g.group_id}
        </Link>
      ),
    },
    { id: "group_name", header: "Name", sortable: true, cell: (g) => g.group_name },
    { id: "vpc_id", header: "VPC", sortable: true, cell: (g) => <IdLink id={g.vpc_id} /> },
    { id: "description", header: "Description", cell: (g) => <span className="text-xs text-muted-foreground">{g.description || "—"}</span> },
    { id: "ingress", header: "Inbound rules", cell: (g) => g.ingress?.length ?? 0 },
    { id: "egress", header: "Outbound rules", cell: (g) => g.egress?.length ?? 0 },
  ];

  const submit = () => {
    setTouched(true);
    if (nameErr || descErr || vpcErr) return;
    setErr(null);
    create.mutate(
      { group_name: form.group_name.trim(), description: form.description.trim(), vpc_id: form.vpc_id },
      {
        onSuccess: (g) => {
          toast.success(`Security group ${g.group_id} created`);
          setOpen(false);
          setForm({ group_name: "", description: "", vpc_id: "" });
          setTouched(false);
          router.push(`/vpc/security-groups/${g.group_id}`);
        },
        onError: (e) => setErr(errorMessage(e)),
      },
    );
  };

  return (
    <div>
      <PageHeader
        title="Security groups"
        description="Stateful instance-level firewalls. New groups get a default allow-all outbound rule."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Create security group
          </Button>
        }
      />
      <ResourceTable
        label="Security groups"
        columns={columns}
        getRowId={(g) => g.group_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        onRowClick={(g) => router.push(`/vpc/security-groups/${g.group_id}`)}
        filters={[{ key: "vpc_id", label: "VPC", options: (vpcs.data ?? []).map((v) => ({ value: v.vpc_id, label: v.vpc_id })) }]}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        emptyTitle="No security groups"
      />
      <FormDialog open={open} onOpenChange={setOpen} title="Create security group" onSubmit={submit} pending={create.isPending} error={err}>
        <FormField label="Security group name" required error={touched ? nameErr : null}>
          {(p) => <Input {...p} value={form.group_name} onChange={(e) => setForm({ ...form, group_name: e.target.value })} />}
        </FormField>
        <FormField label="Description" error={touched ? descErr : null}>
          {(p) => <Input {...p} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />}
        </FormField>
        <FormField label="VPC" required error={touched ? vpcErr : null}>
          {(p) => <VpcSelect {...p} value={form.vpc_id} onChange={(v) => setForm({ ...form, vpc_id: v })} />}
        </FormField>
      </FormDialog>
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title="Delete security group(s)?"
        description={<span className="font-mono">{toDelete.map((g) => g.group_id).join(", ")}</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const g of toDelete) await del.mutateAsync(g.group_id);
        }}
      />
    </div>
  );
}
