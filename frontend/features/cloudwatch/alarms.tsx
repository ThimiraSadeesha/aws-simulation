"use client";

import Link from "next/link";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Plus, Trash2 } from "lucide-react";
import { ReferenceLine, Line, LineChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { StatusBadge } from "@/components/console/status-badge";
import { useListParams } from "@/hooks/use-list-params";
import { formatDate, formatTime } from "@/lib/format";
import type { Alarm, AlarmHistoryItem } from "@/lib/types";
import { TIME_RANGES, useMetricData, useMetricList } from "./metrics-api";
import { useAlarm, useAlarmHistory, useAlarms, useCreateAlarm, useDeleteAlarm } from "./api";
import { CreateAlarmForm } from "./alarm-form";

const OP_SYMBOL: Record<string, string> = {
  GreaterThanThreshold: ">",
  GreaterThanOrEqualToThreshold: ">=",
  LessThanThreshold: "<",
  LessThanOrEqualToThreshold: "<=",
};

export function alarmCondition(a: Pick<Alarm, "statistic" | "metric_name" | "comparison_operator" | "threshold" | "evaluation_periods" | "period">) {
  return `${a.statistic} ${a.metric_name} ${OP_SYMBOL[a.comparison_operator] ?? a.comparison_operator} ${a.threshold} for ${a.evaluation_periods} × ${a.period}s`;
}

function dims(d: Record<string, string> | undefined) {
  const e = Object.entries(d ?? {});
  return e.length ? e.map(([k, v]) => `${k}=${v}`).join(", ") : "—";
}

export function AlarmsPage() {
  const list = useListParams({ sort: "alarm_name" });
  const q = useAlarms(list.params);
  const metrics = useMetricList();
  const create = useCreateAlarm();
  const del = useDeleteAlarm();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [toDelete, setToDelete] = useState<Alarm[]>([]);

  const columns: Column<Alarm>[] = [
    {
      id: "alarm_name",
      header: "Name",
      sortable: true,
      cell: (a) => (
        <Link href={`/cloudwatch/alarms/${encodeURIComponent(a.alarm_name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {a.alarm_name}
        </Link>
      ),
    },
    { id: "state", header: "State", sortable: true, cell: (a) => <StatusBadge status={a.state} /> },
    { id: "metric", header: "Metric", cell: (a) => <span className="text-xs">{a.namespace} / {a.metric_name}</span> },
    { id: "dimensions", header: "Dimensions", cell: (a) => <span className="font-mono text-xs">{dims(a.dimensions)}</span> },
    { id: "condition", header: "Condition", cell: (a) => <span className="text-xs">{alarmCondition(a)}</span> },
    { id: "state_updated_at", header: "State updated", sortable: true, cell: (a) => <span className="whitespace-nowrap">{formatDate(a.state_updated_at)}</span> },
  ];

  return (
    <div>
      <PageHeader
        title="Alarms"
        description="Alarms watch a metric and change state when it crosses a threshold; actions can trigger scaling policies or SNS topics."
        breadcrumbs={[{ label: "CloudWatch", href: "/cloudwatch" }, { label: "Alarms" }]}
      />
      <ResourceTable
        label="Alarms"
        columns={columns}
        getRowId={(a) => a.alarm_name}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        filters={[
          {
            key: "state",
            label: "State",
            options: [
              { value: "OK", label: "OK" },
              { value: "ALARM", label: "ALARM" },
              { value: "INSUFFICIENT_DATA", label: "Insufficient data" },
            ],
          },
        ]}
        selectable
        bulkActions={[{ label: "Delete", destructive: true, icon: <Trash2 />, onClick: (rows) => setToDelete(rows) }]}
        onRowClick={(a) => router.push(`/cloudwatch/alarms/${encodeURIComponent(a.alarm_name)}`)}
        toolbar={
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus /> Create alarm
          </Button>
        }
        emptyTitle="No alarms"
        emptyDescription="Create an alarm on a metric such as CPUUtilization."
      />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Create alarm</DialogTitle>
            <DialogDescription>The alarm is evaluated by the simulator against stored datapoints.</DialogDescription>
          </DialogHeader>
          {open && (
            <CreateAlarmForm
              metrics={metrics.data}
              onCancel={() => setOpen(false)}
              onSubmit={async (input) => {
                await create.mutateAsync(input);
                toast.success(`Alarm ${input.alarm_name} created`);
                setOpen(false);
              }}
            />
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length} alarm(s)?`}
        description={<span className="font-mono text-xs">{toDelete.map((a) => a.alarm_name).join(", ")}</span>}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const a of toDelete) await del.mutateAsync(a.alarm_name);
          toast.success(`Deleted ${toDelete.length} alarm(s)`);
        }}
      />
    </div>
  );
}

export function AlarmDetail({ name }: { name: string }) {
  const q = useAlarm(name);
  const history = useAlarmHistory(name);
  const del = useDeleteAlarm();
  const router = useRouter();
  const [confirm, setConfirm] = useState(false);
  const [range, setRange] = useState(60);
  const alarm = q.data;
  const dimEntry = Object.entries(alarm?.dimensions ?? {})[0];
  const metric = useMetricData(
    alarm
      ? {
          namespace: alarm.namespace,
          metric_name: alarm.metric_name,
          dimension: dimEntry ? `${dimEntry[0]}:${dimEntry[1]}` : undefined,
          period: alarm.period,
          stat: alarm.statistic,
          rangeMinutes: range,
        }
      : null,
  );

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !alarm) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;

  const rows = (metric.data?.datapoints ?? []).map((d) => ({ t: new Date(d.ts).getTime(), v: d.value }));
  const historyColumns: Column<AlarmHistoryItem & { _i: number }>[] = [
    { id: "ts", header: "Time", sortable: true, cell: (h) => <span className="whitespace-nowrap">{formatDate(h.ts)}</span> },
    {
      id: "transition",
      header: "Transition",
      cell: (h) => {
        const from = h.old_state ?? h.from;
        const to = h.new_state ?? h.to;
        return (
          <span className="flex items-center gap-1">
            {from && <StatusBadge status={from} />}
            {from && to && <span aria-hidden>→</span>}
            {to && <StatusBadge status={to} />}
          </span>
        );
      },
    },
    { id: "reason", header: "Reason", cell: (h) => <span className="text-xs">{h.reason ?? h.summary ?? "—"}</span> },
  ];

  return (
    <div className="grid gap-4">
      <PageHeader
        title={alarm.alarm_name}
        description={<StatusBadge status={alarm.state} />}
        breadcrumbs={[
          { label: "CloudWatch", href: "/cloudwatch" },
          { label: "Alarms", href: "/cloudwatch/alarms" },
          { label: alarm.alarm_name },
        ]}
        actions={
          <Button size="sm" variant="destructive" onClick={() => setConfirm(true)}>
            <Trash2 /> Delete
          </Button>
        }
      />
      <Section title="Configuration">
        <DetailGrid
          items={[
            { label: "State", value: <StatusBadge status={alarm.state} /> },
            { label: "State reason", value: <span className="whitespace-normal">{alarm.state_reason || "—"}</span> },
            { label: "State updated", value: formatDate(alarm.state_updated_at) },
            { label: "Metric", value: `${alarm.namespace} / ${alarm.metric_name}` },
            { label: "Dimensions", value: <span className="font-mono text-xs">{dims(alarm.dimensions)}</span> },
            { label: "Condition", value: alarmCondition(alarm) },
            { label: "Actions", value: <span className="font-mono text-xs whitespace-normal">{(alarm.alarm_actions ?? []).join(", ") || "—"}</span> },
            ...(alarm.arn ? [{ label: "ARN", value: <Mono copy>{alarm.arn}</Mono> }] : []),
          ]}
        />
      </Section>
      <Section
        title="Metric vs threshold"
        actions={
          <NativeSelect aria-label="Time range" className="h-7 w-36" value={range} onChange={(e) => setRange(Number(e.target.value))}>
            {TIME_RANGES.map((r) => (
              <option key={r.minutes} value={r.minutes}>
                Last {r.label}
              </option>
            ))}
          </NativeSelect>
        }
      >
        {metric.isError ? (
          <ErrorState error={metric.error} onRetry={() => void metric.refetch()} />
        ) : rows.length === 0 ? (
          <div className="flex h-60 items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground">
            {metric.isLoading ? "Loading…" : "No datapoints in this time range"}
          </div>
        ) : (
          <div role="img" aria-label={`${alarm.metric_name} with threshold ${alarm.threshold}`} className="h-64">
            <ResponsiveContainer width="100%" height="100%">
              <LineChart data={rows} margin={{ top: 10, right: 12, bottom: 0, left: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis
                  dataKey="t"
                  type="number"
                  scale="time"
                  domain={["dataMin", "dataMax"]}
                  tickFormatter={(t: number) => formatTime(new Date(t).toISOString()).slice(0, 5)}
                  tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
                  stroke="var(--border)"
                />
                <YAxis
                  domain={[(min: number) => Math.min(min, alarm.threshold), (max: number) => Math.max(max, alarm.threshold)]}
                  tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
                  stroke="var(--border)"
                  width={48}
                />
                <Tooltip
                  contentStyle={{ background: "var(--popover)", border: "1px solid var(--border)", borderRadius: 6, fontSize: 12 }}
                  labelFormatter={(t) => formatTime(new Date(Number(t)).toISOString())}
                  formatter={(v) => [Number(v).toFixed(2), alarm.metric_name]}
                />
                <ReferenceLine
                  y={alarm.threshold}
                  stroke="var(--chart-5)"
                  strokeDasharray="6 4"
                  label={{ value: `threshold ${alarm.threshold}`, position: "insideTopRight", fontSize: 11, fill: "var(--chart-5)" }}
                />
                <Line type="monotone" dataKey="v" stroke="var(--chart-1)" strokeWidth={1.5} dot={false} isAnimationActive={false} />
              </LineChart>
            </ResponsiveContainer>
          </div>
        )}
      </Section>
      <Section title="History">
        <ResourceTable
          label="Alarm history"
          globalSearch={false}
          columns={historyColumns}
          getRowId={(h) => String(h._i)}
          data={history.data?.map((h, i) => ({ ...h, _i: i }))}
          isLoading={history.isLoading}
          error={history.error}
          onRetry={() => void history.refetch()}
          emptyTitle="No state changes yet"
        />
      </Section>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Delete alarm ${alarm.alarm_name}?`}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          await del.mutateAsync(alarm.alarm_name);
          toast.success("Alarm deleted");
          router.push("/cloudwatch/alarms");
        }}
      />
    </div>
  );
}
