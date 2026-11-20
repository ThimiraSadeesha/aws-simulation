"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormField } from "@/components/console/form-field";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { HostedZone } from "@/lib/types";
import { useCreateZone, useDeleteZone, useZones, zoneId } from "@/features/route53/api";
import { validateDomainName } from "@/features/route53/validation";
import { FormDialog, IdLink, VpcSelect } from "@/features/vpc/components/common";

export default function HostedZonesPage() {
  const list = useListParams();
  const q = useZones(list.params);
  const create = useCreateZone();
  const del = useDeleteZone();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [priv, setPriv] = useState(true);
  const [vpcId, setVpcId] = useState("");
  const [touched, setTouched] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<HostedZone[]>([]);

  const nameErr = validateDomainName(name);
  const vpcErr = priv && !vpcId ? "Private zones must be associated with a VPC." : null;

  const columns: Column<HostedZone>[] = [
    {
      id: "name",
      header: "Domain name",
      sortable: true,
      cell: (z) => (
        <Link href={`/route53/${encodeURIComponent(zoneId(z))}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {z.name}
        </Link>
      ),
    },
    { id: "id", header: "Zone ID", sortable: true, cell: (z) => <Mono copy>{zoneId(z)}</Mono> },
    { id: "private", header: "Type", cell: (z) => (z.private ? "Private" : "Public (simulated)") },
    { id: "vpc_id", header: "VPC", cell: (z) => <IdLink id={z.vpc_id ?? undefined} /> },
    { id: "record_count", header: "Records", cell: (z) => z.record_count ?? "—" },
    { id: "created_at", header: "Created", sortable: true, cell: (z) => <span className="text-xs">{formatDate(z.created_at)}</span> },
  ];

  const reset = () => {
    setName("");
    setVpcId("");
    setTouched(false);
    setErr(null);
  };

  return (
    <div>
      <PageHeader
        title="Route 53 hosted zones"
        description="Simulated DNS zones. Private zone records resolve inside the associated VPC (see VPC → DNS resolver)."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Create hosted zone
          </Button>
        }
      />
      <ResourceTable
        label="Hosted zones"
        columns={columns}
        getRowId={(z) => zoneId(z)}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        onRowClick={(z) => router.push(`/route53/${encodeURIComponent(zoneId(z))}`)}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        emptyTitle="No hosted zones"
      />
      <FormDialog
        open={open}
        onOpenChange={(o) => {
          if (!o) reset();
          setOpen(o);
        }}
        title="Create hosted zone"
        onSubmit={() => {
          setTouched(true);
          if (nameErr || vpcErr) return;
          setErr(null);
          create.mutate(
            { name: name.trim(), private: priv, vpc_id: priv ? vpcId : undefined },
            {
              onSuccess: (z) => {
                toast.success(`Hosted zone ${z.name} created`);
                reset();
                setOpen(false);
              },
              onError: (e) => setErr(errorMessage(e)),
            },
          );
        }}
        pending={create.isPending}
        error={err}
      >
        <FormField label="Domain name" required error={touched ? nameErr : null}>
          {(p) => <Input {...p} className="font-mono" value={name} onChange={(e) => setName(e.target.value)} placeholder="corp.internal" />}
        </FormField>
        <div className="flex items-center gap-3">
          <Switch id="zone-private" checked={priv} onCheckedChange={setPriv} />
          <Label htmlFor="zone-private">Private hosted zone (resolves only inside a VPC)</Label>
        </div>
        {priv && (
          <FormField label="VPC" required error={touched ? vpcErr : null}>
            {(p) => <VpcSelect {...p} value={vpcId} onChange={setVpcId} />}
          </FormField>
        )}
      </FormDialog>
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title="Delete hosted zone(s)?"
        description={<span>{toDelete.map((z) => z.name).join(", ")} and all of their records will be deleted.</span>}
        destructive
        confirmLabel="Delete"
        onConfirm={async () => {
          for (const z of toDelete) await del.mutateAsync(zoneId(z));
        }}
      />
    </div>
  );
}
