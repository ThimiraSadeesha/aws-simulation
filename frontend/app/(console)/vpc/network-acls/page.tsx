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
import type { NetworkAcl } from "@/lib/types";
import { useAllVpcs, useCreateNacl, useDeleteNacl, useNetworkAcls } from "@/features/vpc/api";
import { FormDialog, IdLink, VpcSelect } from "@/features/vpc/components/common";

export default function NetworkAclsPage() {
  const list = useListParams();
  const q = useNetworkAcls(list.params);
  const vpcs = useAllVpcs();
  const create = useCreateNacl();
  const del = useDeleteNacl();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [vpcId, setVpcId] = useState("");
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<NetworkAcl[]>([]);

  const columns: Column<NetworkAcl>[] = [
    {
      id: "network_acl_id",
      header: "Network ACL ID",
      sortable: true,
      cell: (a) => (
        <Link href={`/vpc/network-acls/${a.network_acl_id}`} className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {a.network_acl_id}
        </Link>
      ),
    },
    { id: "name", header: "Name", cell: (a) => a.name || "—" },
    { id: "vpc_id", header: "VPC", sortable: true, cell: (a) => <IdLink id={a.vpc_id} /> },
    { id: "is_default", header: "Default", cell: (a) => (a.is_default ? "Yes" : "No") },
    { id: "entries", header: "Entries", cell: (a) => a.entries?.length ?? 0 },
    { id: "subnet_ids", header: "Associated subnets", cell: (a) => (a.subnet_ids?.length ? a.subnet_ids.join(", ") : "—") },
  ];

  return (
    <div>
      <PageHeader
        title="Network ACLs"
        description="Stateless subnet-level filters evaluated in rule-number order (lowest first). New non-default ACLs deny everything until entries are added."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Create network ACL
          </Button>
        }
      />
      <ResourceTable
        label="Network ACLs"
        columns={columns}
        getRowId={(a) => a.network_acl_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        onRowClick={(a) => router.push(`/vpc/network-acls/${a.network_acl_id}`)}
        filters={[{ key: "vpc_id", label: "VPC", options: (vpcs.data ?? []).map((v) => ({ value: v.vpc_id, label: v.vpc_id })) }]}
        selectable
        bulkActions={[
          { label: "Delete", icon: <Trash2 />, destructive: true, disabled: (rows) => rows.some((r) => r.is_default), onClick: setToDelete },
        ]}
        emptyTitle="No network ACLs"
      />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Create network ACL"
        description="The new ACL denies all traffic until you add allow entries."
        onSubmit={() => {
          if (!vpcId) {
            setErr("Choose a VPC.");
            return;
          }
          setErr(null);
          create.mutate(
            { vpc_id: vpcId, name: name.trim() || undefined },
            {
              onSuccess: (a) => {
                toast.success(`${a.network_acl_id} created`);
                setOpen(false);
                setName("");
                router.push(`/vpc/network-acls/${a.network_acl_id}`);
              },
              onError: (e) => setErr(errorMessage(e)),
            },
          );
        }}
        pending={create.isPending}
        error={err}
      >
        <FormField label="VPC" required>
          {(p) => <VpcSelect {...p} value={vpcId} onChange={setVpcId} />}
        </FormField>
        <FormField label="Name">{(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} />}</FormField>
      </FormDialog>
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title="Delete network ACL(s)?"
        description={<span className="font-mono">{toDelete.map((a) => a.network_acl_id).join(", ")}</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const a of toDelete) await del.mutateAsync(a.network_acl_id);
        }}
      />
    </div>
  );
}
