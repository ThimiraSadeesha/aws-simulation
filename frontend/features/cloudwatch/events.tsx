"use client";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Input } from "@/components/ui/input";
import { Mono } from "@/components/console/copy-button";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { useDebounced } from "@/hooks/use-global-search";
import { useListParams, type ListParamsApi } from "@/hooks/use-list-params";
import { formatDate } from "@/lib/format";
import type { AuditEntry, CloudEvent } from "@/lib/types";
import { useAudit, useEvents } from "./api";
import { useEffect, useState } from "react";

/** Free-text filter input bound (debounced) to a server-side list filter param. */
function TextFilter({ list, param, label, placeholder }: { list: ListParamsApi; param: string; label: string; placeholder: string }) {
  const [v, setV] = useState(list.state.filters[param] ?? "");
  const d = useDebounced(v, 350);
  const { setFilter } = list;
  useEffect(() => {
    setFilter(param, d.trim() || undefined);
  }, [d, param, setFilter]);
  return (
    <label className="flex items-center gap-1 text-xs text-muted-foreground">
      <span>{label}</span>
      <Input aria-label={`Filter by ${label}`} className="h-8 w-48 text-sm" value={v} placeholder={placeholder} onChange={(e) => setV(e.target.value)} />
    </label>
  );
}

const EVENT_TYPES = ["registered", "state_changed", "deregistered", "fault_injected", "fault_triggered", "fault_cleared", "quota_rejected", "scaling"];

function EventsTab() {
  const list = useListParams({ pageSize: 50 });
  const q = useEvents(list.params);
  const columns: Column<CloudEvent>[] = [
    { id: "ts", header: "Time", cell: (e) => <span className="whitespace-nowrap">{formatDate(e.ts)}</span> },
    { id: "resource_id", header: "Resource", cell: (e) => <Mono copy>{e.resource_id}</Mono> },
    { id: "resource_type", header: "Type", cell: (e) => e.resource_type },
    { id: "type", header: "Event", cell: (e) => <span className="font-mono text-xs">{e.type}</span> },
    { id: "message", header: "Message", cell: (e) => <span className="text-xs">{e.message}</span> },
  ];
  return (
    <ResourceTable
      label="Event history"
      columns={columns}
      getRowId={(e) => String(e.id)}
      data={q.data}
      list={list}
      isLoading={q.isLoading}
      isFetching={q.isFetching}
      error={q.error}
      onRetry={() => void q.refetch()}
      hideSearch
      globalSearch={false}
      filters={[{ key: "type", label: "Event", options: EVENT_TYPES.map((t) => ({ value: t, label: t })) }]}
      toolbar={<TextFilter list={list} param="resource_id" label="Resource ID" placeholder="i-0abc…" />}
      emptyTitle="No events"
      emptyDescription="Resource lifecycle events from the simulation engine appear here."
    />
  );
}

function AuditTab() {
  const list = useListParams({ pageSize: 50 });
  const q = useAudit(list.params);
  const columns: Column<AuditEntry>[] = [
    { id: "ts", header: "Time", cell: (a) => <span className="whitespace-nowrap">{formatDate(a.ts)}</span> },
    { id: "principal", header: "Principal", cell: (a) => <span className="font-mono text-xs">{a.principal}</span> },
    { id: "action", header: "Action", cell: (a) => <span className="font-mono text-xs">{a.action}</span> },
    { id: "resource", header: "Resource", cell: (a) => <span className="font-mono text-xs break-all">{a.resource || "—"}</span> },
    { id: "result", header: "Result", cell: (a) => <StatusBadge status={a.result} /> },
    { id: "status_code", header: "Status", cell: (a) => a.status_code },
    { id: "source_ip", header: "Source IP", cell: (a) => <span className="font-mono text-xs">{a.source_ip}</span> },
    { id: "request_id", header: "Request ID", cell: (a) => <Mono copy>{a.request_id}</Mono> },
  ];
  return (
    <ResourceTable
      label="Audit trail"
      columns={columns}
      getRowId={(a) => String(a.id)}
      data={q.data}
      list={list}
      isLoading={q.isLoading}
      isFetching={q.isFetching}
      error={q.error}
      onRetry={() => void q.refetch()}
      hideSearch
      globalSearch={false}
      toolbar={
        <>
          <TextFilter list={list} param="action" label="Action" placeholder="ec2:RunInstances" />
          <TextFilter list={list} param="principal" label="Principal" placeholder="root or iam-user/bob" />
        </>
      }
      emptyTitle="No audit entries"
      emptyDescription="Every API call, including denied ones, is recorded here."
    />
  );
}

export function EventsPage() {
  return (
    <div>
      <PageHeader
        title="Event history"
        description="Resource state changes from the simulation engine, and the API audit trail for your account."
        breadcrumbs={[{ label: "CloudWatch", href: "/cloudwatch" }, { label: "Events" }]}
      />
      <Tabs defaultValue="events">
        <TabsList>
          <TabsTrigger value="events">Resource events</TabsTrigger>
          <TabsTrigger value="audit">API activity (audit)</TabsTrigger>
        </TabsList>
        <TabsContent value="events">
          <EventsTab />
        </TabsContent>
        <TabsContent value="audit">
          <AuditTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
