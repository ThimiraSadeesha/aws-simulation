"use client";

import { useMemo, useState } from "react";
import { Loader2, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { JsonEditor, prettyJson } from "@/components/console/json-editor";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { EmptyState } from "@/components/console/states";
import { errorMessage } from "@/lib/api/client";
import { parsePolicyJson } from "@/lib/validation";
import type { DynamoItem, DynamoTable } from "@/lib/types";
import { getItem, queryItems, useDeleteItem, usePutItem, useScan } from "./api";

const cellText = (v: unknown) => (v === undefined ? "" : typeof v === "object" && v !== null ? JSON.stringify(v) : String(v));

/** Validate an item against the key schema (keys present, N keys numeric). */
export function validateItem(item: Record<string, unknown>, table: DynamoTable): string | null {
  for (const k of [table.partition_key, table.sort_key].filter(Boolean) as { name: string; type: "S" | "N" }[]) {
    const v = item[k.name];
    if (v === undefined || v === null || v === "") return `Item must include key attribute "${k.name}".`;
    if (k.type === "N" && typeof v !== "number") return `Key attribute "${k.name}" must be a number.`;
    if (k.type === "S" && typeof v !== "string") return `Key attribute "${k.name}" must be a string.`;
  }
  return null;
}

function ItemsTable({
  table,
  items,
  label,
  isLoading,
  isFetching,
  error,
  onRetry,
  onEdit,
  onDelete,
  toolbar,
}: {
  table: DynamoTable;
  items: DynamoItem[] | undefined;
  label: string;
  isLoading?: boolean;
  isFetching?: boolean;
  error?: unknown;
  onRetry?: () => void;
  onEdit: (i: DynamoItem) => void;
  onDelete: (i: DynamoItem) => void;
  toolbar?: React.ReactNode;
}) {
  const pk = table.partition_key.name;
  const sk = table.sort_key?.name;
  const columns: Column<DynamoItem>[] = useMemo(() => {
    const keys = new Set<string>();
    for (const it of items ?? []) Object.keys(it).forEach((k) => keys.add(k));
    keys.delete(pk);
    if (sk) keys.delete(sk);
    const attrs = [pk, ...(sk ? [sk] : []), ...[...keys].sort().slice(0, 12)];
    const cols: Column<DynamoItem>[] = attrs.map((a) => ({
      id: a,
      header: a === pk ? `${a} (PK)` : a === sk ? `${a} (SK)` : a,
      sortable: true,
      sortValue: (r) => {
        const v = r[a];
        return typeof v === "number" || typeof v === "string" || typeof v === "boolean" ? v : cellText(v);
      },
      cell: (r) => <span className={`block max-w-64 truncate ${a === pk || a === sk ? "font-mono text-xs" : "text-xs"}`}>{cellText(r[a])}</span>,
    }));
    cols.push({
      id: "__actions",
      header: "",
      className: "text-right whitespace-nowrap",
      cell: (r) => (
        <span className="inline-flex gap-0.5">
          <Button size="icon-sm" variant="ghost" aria-label={`Edit item ${cellText(r[pk])}`} onClick={() => onEdit(r)}>
            <Pencil />
          </Button>
          <Button size="icon-sm" variant="ghost" aria-label={`Delete item ${cellText(r[pk])}`} onClick={() => onDelete(r)}>
            <Trash2 />
          </Button>
        </span>
      ),
    });
    return cols;
  }, [items, pk, sk, onEdit, onDelete]);

  return (
    <ResourceTable<DynamoItem>
      label={label}
      columns={columns}
      getRowId={(r) => `${cellText(r[pk])}\u0000${sk ? cellText(r[sk]) : ""}`}
      data={items}
      isLoading={isLoading}
      isFetching={isFetching}
      error={error}
      onRetry={onRetry}
      globalSearch={false}
      toolbar={toolbar}
      emptyTitle="No items"
    />
  );
}

function ItemEditorDialog({
  table,
  initial,
  open,
  onOpenChange,
}: {
  table: DynamoTable;
  initial: DynamoItem | null;
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const template = () => {
    const t: DynamoItem = { [table.partition_key.name]: table.partition_key.type === "N" ? 1 : "" };
    if (table.sort_key) t[table.sort_key.name] = table.sort_key.type === "N" ? 1 : "";
    return prettyJson(t);
  };
  const [text, setText] = useState(initial ? prettyJson(initial) : template());
  const [error, setError] = useState<string | null>(null);
  const put = usePutItem(table.table_name);
  const save = async () => {
    setError(null);
    const parsed = parsePolicyJson(text);
    if (parsed.error || !parsed.value) {
      setError(parsed.error?.replace("Policy", "Item") ?? "Invalid item");
      return;
    }
    const v = validateItem(parsed.value, table);
    if (v) {
      setError(v);
      return;
    }
    try {
      await put.mutateAsync(parsed.value);
      toast.success("Item saved");
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{initial ? "Edit item" : "Create item"}</DialogTitle>
          <DialogDescription>
            Item JSON. PUT replaces any existing item with the same key ({table.partition_key.name}
            {table.sort_key ? ` + ${table.sort_key.name}` : ""}).
          </DialogDescription>
        </DialogHeader>
        <FormError message={error} />
        <JsonEditor label="Item JSON" value={text} onChange={setText} height={280} />
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={() => void save()} disabled={put.isPending}>
            {put.isPending && <Loader2 className="animate-spin" />} Save item
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function KeyInputs({ table, pk, sk, setPk, setSk, skLabel }: { table: DynamoTable; pk: string; sk: string; setPk: (v: string) => void; setSk: (v: string) => void; skLabel: string }) {
  return (
    <>
      <FormField label={`${table.partition_key.name} (partition key, ${table.partition_key.type})`} required>
        {(p) => <Input {...p} className="h-8" value={pk} onChange={(e) => setPk(e.target.value)} />}
      </FormField>
      {table.sort_key && (
        <FormField label={`${table.sort_key.name} — ${skLabel}`}>
          {(p) => <Input {...p} className="h-8" value={sk} onChange={(e) => setSk(e.target.value)} />}
        </FormField>
      )}
    </>
  );
}

export function TableDetail({ table }: { table: DynamoTable }) {
  const [limit, setLimit] = useState(100);
  const scan = useScan(table.table_name, limit);
  const del = useDeleteItem(table.table_name);
  const [editing, setEditing] = useState<{ item: DynamoItem | null } | null>(null);
  const [toDelete, setToDelete] = useState<DynamoItem | null>(null);

  // Query
  const [qPk, setQPk] = useState("");
  const [qSk, setQSk] = useState("");
  const [qResult, setQResult] = useState<DynamoItem[] | null>(null);
  const [qError, setQError] = useState<string | null>(null);
  const [qPending, setQPending] = useState(false);

  // Get item
  const [gPk, setGPk] = useState("");
  const [gSk, setGSk] = useState("");
  const [gResult, setGResult] = useState<{ item: DynamoItem | null } | null>(null);
  const [gError, setGError] = useState<string | null>(null);
  const [gPending, setGPending] = useState(false);

  const pk = table.partition_key.name;
  const sk = table.sort_key?.name;

  const runQuery = async (e: React.FormEvent) => {
    e.preventDefault();
    setQError(null);
    if (!qPk) {
      setQError("Partition key value is required.");
      return;
    }
    setQPending(true);
    try {
      setQResult(await queryItems(table.table_name, qPk, qSk || undefined));
    } catch (err) {
      setQError(errorMessage(err));
    } finally {
      setQPending(false);
    }
  };

  const runGet = async (e: React.FormEvent) => {
    e.preventDefault();
    setGError(null);
    if (!gPk || (sk && !gSk)) {
      setGError(sk ? "Both key values are required." : "Partition key value is required.");
      return;
    }
    setGPending(true);
    try {
      setGResult({ item: await getItem(table.table_name, gPk, gSk || undefined) });
    } catch (err) {
      setGResult(null);
      setGError(errorMessage(err));
    } finally {
      setGPending(false);
    }
  };

  const onEdit = (item: DynamoItem) => setEditing({ item });
  const onDelete = (item: DynamoItem) => setToDelete(item);

  return (
    <div className="grid gap-4">
      <Section title="Table details">
        <DetailGrid
          items={[
            { label: "Table name", value: table.table_name },
            { label: "Partition key", value: <span className="font-mono text-xs">{`${pk} (${table.partition_key.type})`}</span> },
            { label: "Sort key", value: table.sort_key ? <span className="font-mono text-xs">{`${table.sort_key.name} (${table.sort_key.type})`}</span> : "—" },
          ]}
        />
      </Section>
      <Tabs defaultValue="scan">
        <TabsList>
          <TabsTrigger value="scan">Scan</TabsTrigger>
          <TabsTrigger value="query">Query</TabsTrigger>
          <TabsTrigger value="get">Get item</TabsTrigger>
        </TabsList>
        <TabsContent value="scan" className="mt-3">
          <ItemsTable
            table={table}
            label="Scanned items"
            items={scan.data}
            isLoading={scan.isLoading}
            isFetching={scan.isFetching}
            error={scan.error}
            onRetry={() => void scan.refetch()}
            onEdit={onEdit}
            onDelete={onDelete}
            toolbar={
              <>
                <label className="flex items-center gap-1 text-xs text-muted-foreground">
                  Limit
                  <NativeSelect aria-label="Scan limit" className="h-8 w-20" value={limit} onChange={(e) => setLimit(Number(e.target.value))}>
                    {[25, 100, 500, 1000].map((n) => (
                      <option key={n} value={n}>
                        {n}
                      </option>
                    ))}
                  </NativeSelect>
                </label>
                <Button size="sm" onClick={() => setEditing({ item: null })}>
                  <Plus /> Create item
                </Button>
              </>
            }
          />
        </TabsContent>
        <TabsContent value="query" className="mt-3 grid gap-3">
          <form onSubmit={runQuery} className="grid gap-3 rounded-lg border bg-card p-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
            <KeyInputs table={table} pk={qPk} sk={qSk} setPk={setQPk} setSk={setQSk} skLabel="begins with (optional)" />
            <Button type="submit" disabled={qPending}>
              {qPending ? <Loader2 className="animate-spin" /> : <Search />} Run query
            </Button>
            <div className="sm:col-span-3">
              <FormError message={qError} />
            </div>
          </form>
          {qResult && <ItemsTable table={table} label="Query results" items={qResult} onEdit={onEdit} onDelete={onDelete} />}
        </TabsContent>
        <TabsContent value="get" className="mt-3 grid gap-3">
          <form onSubmit={runGet} className="grid gap-3 rounded-lg border bg-card p-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
            <KeyInputs table={table} pk={gPk} sk={gSk} setPk={setGPk} setSk={setGSk} skLabel="sort key value" />
            <Button type="submit" disabled={gPending}>
              {gPending ? <Loader2 className="animate-spin" /> : <Search />} Get item
            </Button>
            <div className="sm:col-span-3">
              <FormError message={gError} />
            </div>
          </form>
          {gResult &&
            (gResult.item ? (
              <Section
                title="Item"
                actions={
                  <>
                    <Button size="sm" variant="outline" onClick={() => onEdit(gResult.item!)}>
                      <Pencil /> Edit
                    </Button>
                    <Button size="sm" variant="destructive" onClick={() => onDelete(gResult.item!)}>
                      <Trash2 /> Delete
                    </Button>
                  </>
                }
              >
                <pre className="overflow-auto rounded-md bg-muted/50 p-3 font-mono text-xs">{prettyJson(gResult.item)}</pre>
              </Section>
            ) : (
              <EmptyState title="Item not found" />
            ))}
        </TabsContent>
      </Tabs>

      {editing && (
        <ItemEditorDialog
          key={editing.item ? JSON.stringify(editing.item) : "new"}
          table={table}
          initial={editing.item}
          open
          onOpenChange={(o) => !o && setEditing(null)}
        />
      )}
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Delete item?"
        destructive
        confirmLabel="Delete item"
        description={
          <span className="font-mono text-xs">
            {pk}={cellText(toDelete?.[pk])}
            {sk ? `, ${sk}=${cellText(toDelete?.[sk])}` : ""}
          </span>
        }
        onConfirm={async () => {
          await del.mutateAsync({ pk: cellText(toDelete![pk]), sk: sk ? cellText(toDelete![sk]) : undefined });
          if (gResult?.item === toDelete) setGResult(null);
          if (qResult) setQResult(qResult.filter((i) => i !== toDelete));
        }}
      />
    </div>
  );
}
