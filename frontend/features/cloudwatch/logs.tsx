"use client";

import Link from "next/link";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, Plus, Search, Send, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { EmptyState, ErrorState, LoadingState } from "@/components/console/states";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatBytes, formatDate } from "@/lib/format";
import type { LogGroup, LogStream } from "@/lib/types";
import { streamName, useCreateLogGroup, useDeleteLogGroup, useLogEvents, useLogGroups, useLogStreams, usePutLogEvents, type LogEventsQuery } from "./api";

export const logGroupHref = (name: string) => `/cloudwatch/logs/${encodeURIComponent(name)}`;

const RETENTION = [1, 3, 5, 7, 14, 30, 60, 90, 180, 365];

export function LogGroupsPage() {
  const list = useListParams({ sort: "name" });
  const q = useLogGroups(list.params);
  const del = useDeleteLogGroup();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [toDelete, setToDelete] = useState<LogGroup[]>([]);

  const columns: Column<LogGroup>[] = [
    {
      id: "name",
      header: "Log group",
      sortable: true,
      cell: (g) => (
        <Link href={logGroupHref(g.name)} className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {g.name}
        </Link>
      ),
    },
    { id: "retention_days", header: "Retention", sortable: true, cell: (g) => (g.retention_days ? `${g.retention_days} days` : "Never expire") },
    { id: "stored_bytes", header: "Stored", sortable: true, cell: (g) => (g.stored_bytes !== undefined ? formatBytes(g.stored_bytes) : "—") },
    { id: "created_at", header: "Created", sortable: true, cell: (g) => formatDate(g.created_at) },
  ];

  return (
    <div>
      <PageHeader
        title="Log groups"
        description="The platform writes instance boot logs to /cloudsim/ec2/console and the audit trail to /cloudsim/audit."
        breadcrumbs={[{ label: "CloudWatch", href: "/cloudwatch" }, { label: "Log groups" }]}
      />
      <ResourceTable
        label="Log groups"
        columns={columns}
        getRowId={(g) => g.name}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        selectable
        bulkActions={[{ label: "Delete", destructive: true, icon: <Trash2 />, onClick: (rows) => setToDelete(rows) }]}
        onRowClick={(g) => router.push(logGroupHref(g.name))}
        toolbar={
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus /> Create log group
          </Button>
        }
        emptyTitle="No log groups"
      />
      <CreateLogGroupDialog open={open} onOpenChange={setOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length} log group(s)?`}
        description={
          <span>
            All streams and events are permanently deleted: <span className="font-mono text-xs">{toDelete.map((g) => g.name).join(", ")}</span>
          </span>
        }
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const g of toDelete) await del.mutateAsync(g.name);
          toast.success(`Deleted ${toDelete.length} log group(s)`);
        }}
      />
    </div>
  );
}

function CreateLogGroupDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const create = useCreateLogGroup();
  const [name, setName] = useState("");
  const [retention, setRetention] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!/^[\w\-_/.#]{1,512}$/.test(name)) return setErr("Log group names are 1–512 characters: letters, digits, '_', '-', '/', '.', '#'.");
    setErr(null);
    try {
      await create.mutateAsync({ name, ...(retention ? { retention_days: Number(retention) } : {}) });
      toast.success(`Log group ${name} created`);
      setName("");
      setRetention("");
      onOpenChange(false);
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create log group</DialogTitle>
          <DialogDescription>Streams are created automatically when you put events into them.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="grid gap-3" noValidate>
          <FormError message={err} />
          <FormField label="Name">{(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} placeholder="/myapp/web" />}</FormField>
          <FormField label="Retention">
            {(p) => (
              <NativeSelect {...p} value={retention} onChange={(e) => setRetention(e.target.value)}>
                <option value="">Never expire</option>
                {RETENTION.map((d) => (
                  <option key={d} value={d}>
                    {d} days
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function toIso(local: string): string | undefined {
  if (!local) return undefined;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

export function LogGroupDetail({ name }: { name: string }) {
  const streams = useLogStreams(name);
  const del = useDeleteLogGroup();
  const router = useRouter();
  const [confirm, setConfirm] = useState(false);
  const [draft, setDraft] = useState({ filter: "", stream: "", start: "", end: "", limit: "100" });
  const [applied, setApplied] = useState<LogEventsQuery>({ limit: 100 });
  const [live, setLive] = useState(false);
  const events = useLogEvents(name, applied, live ? 5000 : false);

  const apply = (e?: React.FormEvent) => {
    e?.preventDefault();
    const limit = Math.min(10_000, Math.max(1, Number(draft.limit) || 100));
    setApplied({
      ...(draft.filter.trim() ? { filter_pattern: draft.filter.trim() } : {}),
      ...(draft.stream ? { stream: draft.stream } : {}),
      ...(toIso(draft.start) ? { start: toIso(draft.start) } : {}),
      ...(toIso(draft.end) ? { end: toIso(draft.end) } : {}),
      limit,
    });
  };

  const streamColumns: Column<LogStream>[] = [
    {
      id: "name",
      header: "Stream",
      sortable: true,
      sortValue: (s) => streamName(s),
      cell: (s) => (
        <button
          type="button"
          className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400"
          onClick={() => {
            setDraft((d) => ({ ...d, stream: streamName(s) }));
            setApplied((a) => ({ ...a, stream: streamName(s) }));
          }}
        >
          {streamName(s)}
        </button>
      ),
    },
    { id: "last_event_ts", header: "Last event", sortable: true, cell: (s) => formatDate(s.last_event_ts) },
    { id: "created_at", header: "Created", sortable: true, cell: (s) => formatDate(s.created_at) },
  ];

  return (
    <div className="grid gap-4">
      <PageHeader
        title={<span className="font-mono">{name}</span>}
        breadcrumbs={[
          { label: "CloudWatch", href: "/cloudwatch" },
          { label: "Log groups", href: "/cloudwatch/logs" },
          { label: name },
        ]}
        actions={
          <Button size="sm" variant="destructive" onClick={() => setConfirm(true)}>
            <Trash2 /> Delete log group
          </Button>
        }
      />
      <Section title="Log streams">
        <ResourceTable
          label="Log streams"
          globalSearch={false}
          columns={streamColumns}
          getRowId={(s) => streamName(s)}
          data={streams.data}
          isLoading={streams.isLoading}
          isFetching={streams.isFetching}
          error={streams.error}
          onRetry={() => void streams.refetch()}
          searchText={(s) => streamName(s)}
          emptyTitle="No streams yet"
          emptyDescription="Put an event below to create a stream."
        />
      </Section>
      <Section
        title="Log events"
        actions={
          <div className="flex items-center gap-2">
            <Switch id="logs-live" checked={live} onCheckedChange={setLive} />
            <Label htmlFor="logs-live" className="text-xs font-normal">
              Live tail (5 s)
            </Label>
          </div>
        }
      >
        <form onSubmit={apply} className="grid gap-3" noValidate>
          <FormField
            label="Filter pattern"
            hint={
              <>
                Terms must all match (<code>error timeout</code>), <code>&quot;quoted phrase&quot;</code> matches exactly, <code>-term</code> excludes,{" "}
                <code>?a ?b</code> matches any of. Leave empty for all events.
              </>
            }
          >
            {(p) => <Input {...p} className="font-mono" value={draft.filter} onChange={(e) => setDraft((d) => ({ ...d, filter: e.target.value }))} placeholder='"boot completed" -debug' />}
          </FormField>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <FormField label="Stream">
              {(p) => (
                <NativeSelect {...p} value={draft.stream} onChange={(e) => setDraft((d) => ({ ...d, stream: e.target.value }))}>
                  <option value="">All streams</option>
                  {(streams.data ?? []).map((s) => (
                    <option key={streamName(s)} value={streamName(s)}>
                      {streamName(s)}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </FormField>
            <FormField label="Start">
              {(p) => <Input {...p} type="datetime-local" value={draft.start} onChange={(e) => setDraft((d) => ({ ...d, start: e.target.value }))} />}
            </FormField>
            <FormField label="End">
              {(p) => <Input {...p} type="datetime-local" value={draft.end} onChange={(e) => setDraft((d) => ({ ...d, end: e.target.value }))} />}
            </FormField>
            <FormField label="Limit">
              {(p) => <Input {...p} type="number" min={1} max={10000} value={draft.limit} onChange={(e) => setDraft((d) => ({ ...d, limit: e.target.value }))} />}
            </FormField>
          </div>
          <div>
            <Button type="submit" size="sm">
              <Search /> Search events
            </Button>
          </div>
        </form>
        <div className="mt-4">
          {events.isLoading ? (
            <LoadingState />
          ) : events.isError ? (
            <ErrorState error={events.error} onRetry={() => void events.refetch()} />
          ) : (events.data ?? []).length === 0 ? (
            <EmptyState title="No matching events" description="Adjust the filter pattern, stream or time range." />
          ) : (
            <div className="max-h-[32rem] overflow-auto rounded-md border bg-muted/30" aria-label="Log events" role="log">
              <table className="w-full text-xs">
                <thead className="sticky top-0 bg-muted">
                  <tr>
                    <th scope="col" className="px-2 py-1 text-left font-semibold whitespace-nowrap">
                      Timestamp
                    </th>
                    {!applied.stream && (
                      <th scope="col" className="px-2 py-1 text-left font-semibold">
                        Stream
                      </th>
                    )}
                    <th scope="col" className="px-2 py-1 text-left font-semibold">
                      Message
                    </th>
                  </tr>
                </thead>
                <tbody className="font-mono">
                  {(events.data ?? []).map((ev, i) => (
                    <tr key={`${ev.id ?? i}-${ev.ts}`} className="border-t align-top">
                      <td className="px-2 py-1 whitespace-nowrap text-muted-foreground">{formatDate(ev.ts)}</td>
                      {!applied.stream && <td className="px-2 py-1 whitespace-nowrap text-muted-foreground">{ev.stream ?? "—"}</td>}
                      <td className="px-2 py-1 break-all whitespace-pre-wrap">{ev.message}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {events.data && <p className="mt-1 text-xs text-muted-foreground">{events.data.length} event(s)</p>}
        </div>
      </Section>
      <PutEventsForm group={name} streams={(streams.data ?? []).map(streamName)} />
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Delete log group ${name}?`}
        description="All streams and events are permanently deleted."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          await del.mutateAsync(name);
          toast.success("Log group deleted");
          router.push("/cloudwatch/logs");
        }}
      />
    </div>
  );
}

function PutEventsForm({ group, streams }: { group: string; streams: string[] }) {
  const put = usePutLogEvents(group);
  const [stream, setStream] = useState("");
  const [message, setMessage] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!/^[^:*]{1,512}$/.test(stream)) return setErr("Stream name is required (1–512 characters, no ':' or '*').");
    if (!message.trim()) return setErr("Message is required.");
    setErr(null);
    try {
      const events = message
        .split("\n")
        .filter((l) => l.trim())
        .map((m) => ({ message: m, ts: new Date().toISOString() }));
      await put.mutateAsync({ stream, events });
      toast.success(`Put ${events.length} event(s) into ${stream}`);
      setMessage("");
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  };

  return (
    <Section title="Put log events">
      <form onSubmit={submit} className="grid gap-3" noValidate>
        <FormError message={err} />
        <div className="grid gap-3 sm:grid-cols-[16rem_1fr] sm:items-start">
          <FormField label="Stream name" hint="Existing or new stream.">
            {(p) => (
              <>
                <Input {...p} list="log-streams-list" value={stream} onChange={(e) => setStream(e.target.value)} placeholder="app-1" />
                <datalist id="log-streams-list">
                  {streams.map((s) => (
                    <option key={s} value={s} />
                  ))}
                </datalist>
              </>
            )}
          </FormField>
          <FormField label="Message" hint="One event per line.">
            {(p) => <Textarea {...p} rows={3} className="font-mono text-xs" value={message} onChange={(e) => setMessage(e.target.value)} placeholder="ERROR payment timeout order=42" />}
          </FormField>
        </div>
        <div>
          <Button type="submit" size="sm" disabled={put.isPending}>
            {put.isPending ? <Loader2 className="animate-spin" /> : <Send />} Put events
          </Button>
        </div>
      </form>
    </Section>
  );
}
