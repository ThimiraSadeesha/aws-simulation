"use client";

import Link from "next/link";
import { useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { Section } from "@/components/console/detail-grid";
import { ErrorState, LoadingState } from "@/components/console/states";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatBytes, formatDate, formatNumber } from "@/lib/format";
import { validateBucketName } from "@/lib/validation";
import type { Bucket } from "@/lib/types";
import { useBuckets, useCreateBucket, useDeleteBucket, useS3Stats } from "./api";

export function CreateBucketDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [name, setName] = useState("");
  const [acl, setAcl] = useState<"private" | "public-read">("private");
  const [touched, setTouched] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);
  const create = useCreateBucket();
  const nameError = touched ? validateBucketName(name) : null;

  const close = (o: boolean) => {
    if (!o) {
      setName("");
      setAcl("private");
      setTouched(false);
      setServerError(null);
    }
    onOpenChange(o);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setTouched(true);
    setServerError(null);
    if (validateBucketName(name)) return;
    try {
      await create.mutateAsync({ name, acl });
      close(false);
    } catch (err) {
      setServerError(errorMessage(err));
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create bucket</DialogTitle>
            <DialogDescription>Bucket names are globally unique across this CloudSim platform and must be DNS-compatible.</DialogDescription>
          </DialogHeader>
          <FormError message={serverError} />
          <FormField label="Bucket name" required error={nameError} hint="3–63 characters: lowercase letters, digits, dots and hyphens.">
            {(p) => (
              <Input {...p} value={name} onChange={(e) => setName(e.target.value)} onBlur={() => setTouched(true)} autoComplete="off" autoFocus />
            )}
          </FormField>
          <FormField label="Access control (ACL)">
            {(p) => (
              <NativeSelect {...p} value={acl} onChange={(e) => setAcl(e.target.value as typeof acl)}>
                <option value="private">private</option>
                <option value="public-read">public-read</option>
              </NativeSelect>
            )}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create bucket
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function DeleteBucketDialog({ bucket, onDone }: { bucket: string | null; onDone: (deleted: boolean) => void }) {
  const [force, setForce] = useState(false);
  const del = useDeleteBucket();
  return (
    <ConfirmDialog
      open={!!bucket}
      onOpenChange={(o) => {
        if (!o) {
          setForce(false);
          onDone(false);
        }
      }}
      title={`Delete bucket ${bucket ?? ""}?`}
      destructive
      confirmLabel="Delete bucket"
      typeToConfirm={force ? bucket ?? undefined : undefined}
      description="A bucket can only be deleted when it is empty, unless you force-delete it, which also permanently removes every object and version."
      onConfirm={async () => {
        await del.mutateAsync({ name: bucket!, force });
        setForce(false);
        onDone(true);
      }}
    >
      <div className="flex items-center gap-2">
        <Checkbox id="force-delete" checked={force} onCheckedChange={(c) => setForce(c === true)} />
        <Label htmlFor="force-delete" className="text-sm">
          Force delete (empty the bucket first)
        </Label>
      </div>
    </ConfirmDialog>
  );
}

function StorageStats() {
  const stats = useS3Stats();
  return (
    <Section title="Storage">
      {stats.isLoading ? (
        <LoadingState />
      ) : stats.isError ? (
        <ErrorState error={stats.error} onRetry={() => void stats.refetch()} compact />
      ) : stats.data ? (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,14rem)_1fr]">
          <dl className="grid grid-cols-2 gap-3 text-sm lg:grid-cols-1">
            <div>
              <dt className="text-xs text-muted-foreground">Buckets</dt>
              <dd className="text-lg font-semibold">{formatNumber(stats.data.buckets)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Objects / versions</dt>
              <dd className="text-lg font-semibold">
                {formatNumber(stats.data.objects)} / {formatNumber(stats.data.versions)}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Total size</dt>
              <dd className="text-lg font-semibold">{formatBytes(stats.data.size_bytes)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">By storage class</dt>
              <dd className="text-xs">
                {Object.entries(stats.data.by_storage_class ?? {}).length === 0
                  ? "—"
                  : Object.entries(stats.data.by_storage_class).map(([k, v]) => (
                      <div key={k} className="flex justify-between gap-2">
                        <span className="font-mono">{k}</span>
                        <span>{formatBytes(v)}</span>
                      </div>
                    ))}
              </dd>
            </div>
          </dl>
          <div>
            <div className="mb-1 text-xs text-muted-foreground">Size by bucket</div>
            {(stats.data.by_bucket ?? []).length === 0 ? (
              <div className="flex h-40 items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground">No buckets yet</div>
            ) : (
              <div role="img" aria-label="Bar chart of storage size by bucket" className="h-48">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={stats.data.by_bucket} margin={{ top: 5, right: 8, bottom: 0, left: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="name" tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" />
                    <YAxis tickFormatter={(v: number) => formatBytes(v, 0)} tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" width={64} />
                    <Tooltip
                      contentStyle={{ background: "var(--popover)", border: "1px solid var(--border)", borderRadius: 6, fontSize: 12 }}
                      formatter={(v, _n, item) => [`${formatBytes(Number(v))} · ${formatNumber((item?.payload as { objects?: number })?.objects)} objects`, "Size"]}
                    />
                    <Bar dataKey="size_bytes" fill="var(--chart-1)" radius={[3, 3, 0, 0]} isAnimationActive={false} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            )}
          </div>
        </div>
      ) : null}
    </Section>
  );
}

export function BucketList() {
  const list = useListParams({ sort: "name" });
  const q = useBuckets(list.params);
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<string | null>(null);

  const columns: Column<Bucket>[] = [
    {
      id: "name",
      header: "Name",
      sortable: true,
      cell: (r) => (
        <Link href={`/s3/${encodeURIComponent(r.name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
          {r.name}
        </Link>
      ),
    },
    { id: "region", header: "Region", cell: (r) => <span className="font-mono text-xs">{r.region}</span> },
    { id: "acl", header: "Access", cell: (r) => (r.acl === "public-read" ? <StatusBadge status="degraded" label="public-read" /> : "private") },
    { id: "versioning", header: "Versioning", cell: (r) => <StatusBadge status={r.versioning} /> },
    { id: "object_count", header: "Objects", sortable: true, cell: (r) => formatNumber(r.object_count) },
    { id: "size_bytes", header: "Size", sortable: true, cell: (r) => formatBytes(r.size_bytes) },
    { id: "created_at", header: "Created", sortable: true, cell: (r) => <span className="text-xs">{formatDate(r.created_at)}</span> },
    {
      id: "actions",
      header: "",
      cell: (r) => (
        <Button size="icon-sm" variant="ghost" aria-label={`Delete bucket ${r.name}`} onClick={() => setToDelete(r.name)}>
          <Trash2 />
        </Button>
      ),
    },
  ];

  return (
    <div className="grid gap-4">
      <PageHeader
        title="S3 — Buckets"
        description="Simulated object storage. Buckets and objects live in the local CloudSim database."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> Create bucket
          </Button>
        }
      />
      <StorageStats />
      <ResourceTable<Bucket>
        label="Buckets"
        columns={columns}
        getRowId={(r) => r.name}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        emptyTitle="No buckets"
        emptyDescription="Create a bucket to start storing objects."
        emptyAction={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create bucket
          </Button>
        }
      />
      <CreateBucketDialog open={createOpen} onOpenChange={setCreateOpen} />
      <DeleteBucketDialog bucket={toDelete} onDone={() => setToDelete(null)} />
    </div>
  );
}
