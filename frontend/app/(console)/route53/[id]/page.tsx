"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { PageHeader } from "@/components/console/page-header";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormField, NativeSelect } from "@/components/console/form-field";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import type { DnsRecord } from "@/lib/types";
import { useCreateRecord, useDeleteRecord, useRecords, useZone } from "@/features/route53/api";
import { validateDomainName, validateRecordValues } from "@/features/route53/validation";
import { FormDialog, IdLink } from "@/features/vpc/components/common";

export default function ZoneDetailPage() {
  const { id: rawId } = useParams<{ id: string }>();
  const id = decodeURIComponent(rawId);
  const zone = useZone(id);
  const list = useListParams();
  const records = useRecords(id, list.params);
  const create = useCreateRecord(id);
  const del = useDeleteRecord(id);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ name: "", type: "A" as DnsRecord["type"], ttl: "300", values: "" });
  const [touched, setTouched] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<DnsRecord | null>(null);

  const z = zone.data;
  const values = form.values
    .split("\n")
    .map((v) => v.trim())
    .filter(Boolean);
  const fullName = form.name.trim() ? (z && !form.name.trim().endsWith(z.name.replace(/\.$/, "")) ? `${form.name.trim()}.${z.name.replace(/\.$/, "")}` : form.name.trim()) : z?.name ?? "";
  const nameErr = validateDomainName(fullName);
  const ttl = Number(form.ttl);
  const ttlErr = Number.isInteger(ttl) && ttl >= 0 && ttl <= 2147483647 ? null : "TTL must be a non-negative whole number of seconds.";
  const valErr = validateRecordValues(form.type, values);

  const cols: Column<DnsRecord>[] = [
    { id: "name", header: "Record name", sortable: true, cell: (r) => <span className="font-mono text-xs">{r.name}</span> },
    { id: "type", header: "Type", sortable: true, cell: (r) => r.type },
    { id: "ttl", header: "TTL", sortable: true, cell: (r) => r.ttl },
    { id: "values", header: "Values", cell: (r) => <span className="font-mono text-xs whitespace-pre-line">{r.values.join("\n")}</span> },
    {
      id: "actions",
      header: "",
      cell: (r) => (
        <Button size="icon-xs" variant="ghost" aria-label={`Delete ${r.type} record ${r.name}`} onClick={() => setToDelete(r)}>
          <Trash2 />
        </Button>
      ),
    },
  ];

  return (
    <div className="grid gap-4">
      <PageHeader
        breadcrumbs={[{ label: "Route 53", href: "/route53" }, { label: z?.name ?? id }]}
        title={z?.name ?? id}
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Create record
          </Button>
        }
      />
      {z && (
        <Section title="Hosted zone details">
          <DetailGrid
            items={[
              { label: "Zone ID", value: <Mono copy>{id}</Mono> },
              { label: "Type", value: z.private ? "Private" : "Public (simulated)" },
              { label: "VPC", value: <IdLink id={z.vpc_id ?? undefined} /> },
            ]}
          />
        </Section>
      )}
      <ResourceTable
        label="Records"
        columns={cols}
        getRowId={(r) => `${r.name}|${r.type}`}
        data={records.data}
        list={list}
        isLoading={records.isLoading}
        isFetching={records.isFetching}
        error={records.error}
        onRetry={() => void records.refetch()}
        filters={[{ key: "type", label: "Type", options: ["A", "CNAME", "TXT"].map((t) => ({ value: t, label: t })) }]}
        emptyTitle="No records"
      />
      <FormDialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) {
            setTouched(false);
            setErr(null);
          }
        }}
        title="Create record"
        onSubmit={() => {
          setTouched(true);
          if (nameErr || ttlErr || valErr) return;
          setErr(null);
          create.mutate(
            { name: fullName, type: form.type, ttl, values },
            {
              onSuccess: () => {
                toast.success(`${form.type} record ${fullName} created`);
                setForm({ name: "", type: "A", ttl: "300", values: "" });
                setTouched(false);
                setOpen(false);
              },
              onError: (e) => setErr(errorMessage(e)),
            },
          );
        }}
        pending={create.isPending}
        error={err}
      >
        <FormField label="Record name" hint={z ? `Full name: ${fullName || z.name}` : undefined} error={touched ? nameErr : null}>
          {(p) => <Input {...p} className="font-mono" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="www" />}
        </FormField>
        <div className="grid grid-cols-2 gap-3">
          <FormField label="Type">
            {(p) => (
              <NativeSelect {...p} value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value as DnsRecord["type"] })}>
                <option value="A">A — IPv4 address</option>
                <option value="CNAME">CNAME — alias</option>
                <option value="TXT">TXT — text</option>
              </NativeSelect>
            )}
          </FormField>
          <FormField label="TTL (seconds)" error={touched ? ttlErr : null}>
            {(p) => <Input {...p} type="number" min={0} value={form.ttl} onChange={(e) => setForm({ ...form, ttl: e.target.value })} />}
          </FormField>
        </div>
        <FormField label="Values" hint="One value per line." error={touched ? valErr : null}>
          {(p) => <Textarea {...p} className="font-mono text-xs" rows={4} value={form.values} onChange={(e) => setForm({ ...form, values: e.target.value })} />}
        </FormField>
      </FormDialog>
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Delete record?"
        description={
          <span className="font-mono">
            {toDelete?.name} {toDelete?.type}
          </span>
        }
        destructive
        confirmLabel="Delete"
        onConfirm={() => del.mutateAsync({ name: toDelete!.name, type: toDelete!.type })}
      />
    </div>
  );
}
