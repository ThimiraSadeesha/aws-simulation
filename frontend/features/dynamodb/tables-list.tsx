"use client";

import Link from "next/link";
import { useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
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
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate, formatNumber } from "@/lib/format";
import type { DynamoTable } from "@/lib/types";
import { useCreateTable, useDeleteTable, useTables } from "./api";

export const TABLE_NAME_RE = /^[A-Za-z0-9_.-]{3,255}$/;
const ATTR_RE = /^[A-Za-z0-9_.-]{1,255}$/;

export function validateTableForm(f: { table_name: string; pk: string; useSort: boolean; sk: string }) {
  const e: Record<string, string> = {};
  if (!TABLE_NAME_RE.test(f.table_name)) e.table_name = "3–255 characters: letters, digits, '_', '-', '.'.";
  if (!ATTR_RE.test(f.pk)) e.pk = "Partition key name is required (letters, digits, '_', '-', '.').";
  if (f.useSort && !ATTR_RE.test(f.sk)) e.sk = "Sort key name is required.";
  if (f.useSort && f.sk === f.pk) e.sk = "Sort key must differ from the partition key.";
  return e;
}

function CreateTableDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const empty = { table_name: "", pk: "", pkType: "S" as "S" | "N", useSort: false, sk: "", skType: "S" as "S" | "N" };
  const [f, setF] = useState(empty);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [serverError, setServerError] = useState<string | null>(null);
  const create = useCreateTable();
  const close = (o: boolean) => {
    if (!o) {
      setF(empty);
      setErrors({});
      setServerError(null);
    }
    onOpenChange(o);
  };
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const v = validateTableForm(f);
    setErrors(v);
    setServerError(null);
    if (Object.keys(v).length) return;
    try {
      await create.mutateAsync({
        table_name: f.table_name,
        partition_key: { name: f.pk, type: f.pkType },
        ...(f.useSort ? { sort_key: { name: f.sk, type: f.skType } } : {}),
      });
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
            <DialogTitle>Create table</DialogTitle>
            <DialogDescription>A simulated key-value / document table with a partition key and optional sort key.</DialogDescription>
          </DialogHeader>
          <FormError message={serverError} />
          <FormField label="Table name" required error={errors.table_name}>
            {(p) => <Input {...p} value={f.table_name} onChange={(e) => setF({ ...f, table_name: e.target.value })} autoFocus />}
          </FormField>
          <div className="grid grid-cols-[1fr_7rem] gap-2">
            <FormField label="Partition key" required error={errors.pk}>
              {(p) => <Input {...p} value={f.pk} onChange={(e) => setF({ ...f, pk: e.target.value })} placeholder="e.g. user_id" />}
            </FormField>
            <FormField label="Type">
              {(p) => (
                <NativeSelect {...p} value={f.pkType} onChange={(e) => setF({ ...f, pkType: e.target.value as "S" | "N" })}>
                  <option value="S">String</option>
                  <option value="N">Number</option>
                </NativeSelect>
              )}
            </FormField>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="use-sort-key" checked={f.useSort} onCheckedChange={(c) => setF({ ...f, useSort: c === true })} />
            <Label htmlFor="use-sort-key" className="text-sm">
              Add a sort key
            </Label>
          </div>
          {f.useSort && (
            <div className="grid grid-cols-[1fr_7rem] gap-2">
              <FormField label="Sort key" required error={errors.sk}>
                {(p) => <Input {...p} value={f.sk} onChange={(e) => setF({ ...f, sk: e.target.value })} placeholder="e.g. created_at" />}
              </FormField>
              <FormField label="Type">
                {(p) => (
                  <NativeSelect {...p} value={f.skType} onChange={(e) => setF({ ...f, skType: e.target.value as "S" | "N" })}>
                    <option value="S">String</option>
                    <option value="N">Number</option>
                  </NativeSelect>
                )}
              </FormField>
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create table
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const keyLabel = (k?: { name: string; type: string } | null) => (k ? `${k.name} (${k.type})` : "—");

export function TablesList() {
  const list = useListParams({ sort: "table_name" });
  const q = useTables(list.params);
  const del = useDeleteTable();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<string | null>(null);
  const columns: Column<DynamoTable>[] = [
    {
      id: "table_name",
      header: "Table name",
      sortable: true,
      cell: (r) => (
        <Link href={`/dynamodb/${encodeURIComponent(r.table_name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
          {r.table_name}
        </Link>
      ),
    },
    { id: "status", header: "Status", cell: (r) => <StatusBadge status={r.status ?? "active"} /> },
    { id: "partition_key", header: "Partition key", cell: (r) => <span className="font-mono text-xs">{keyLabel(r.partition_key)}</span> },
    { id: "sort_key", header: "Sort key", cell: (r) => <span className="font-mono text-xs">{keyLabel(r.sort_key)}</span> },
    { id: "item_count", header: "Items", sortable: true, cell: (r) => formatNumber(r.item_count) },
    { id: "created_at", header: "Created", sortable: true, cell: (r) => <span className="text-xs">{formatDate(r.created_at)}</span> },
    {
      id: "actions",
      header: "",
      className: "text-right",
      cell: (r) => (
        <Button size="icon-sm" variant="ghost" aria-label={`Delete table ${r.table_name}`} onClick={() => setToDelete(r.table_name)}>
          <Trash2 />
        </Button>
      ),
    },
  ];
  return (
    <div className="grid gap-4">
      <PageHeader
        title="DynamoDB — Tables"
        description="Simulated NoSQL tables with partition/sort keys, item CRUD, query and scan."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> Create table
          </Button>
        }
      />
      <ResourceTable<DynamoTable>
        label="Tables"
        columns={columns}
        getRowId={(r) => r.table_name}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        emptyTitle="No tables"
        emptyDescription="Create a table to store items."
      />
      <CreateTableDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete table ${toDelete ?? ""}?`}
        destructive
        confirmLabel="Delete table"
        typeToConfirm={toDelete ?? undefined}
        description="The table and all of its items are permanently deleted."
        onConfirm={() => del.mutateAsync(toDelete!)}
      />
    </div>
  );
}
