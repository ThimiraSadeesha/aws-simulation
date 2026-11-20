"use client";

import Link from "next/link";
import { Activity, Bell, Boxes, HardDrive, Network, Scale, Server, Gauge } from "lucide-react";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Progress } from "@/components/ui/progress";
import { PageHeader } from "@/components/console/page-header";
import { Section } from "@/components/console/detail-grid";
import { MetricChart } from "@/components/console/metric-chart";
import { StatusBadge } from "@/components/console/status-badge";
import { ErrorState, LoadingState } from "@/components/console/states";
import { formatBytes, formatNumber, formatPercent, formatTime, relativeTime } from "@/lib/format";
import type { DashboardSummary } from "@/lib/types";
import { useDashboardSummary, useHealthz } from "@/features/dashboard/api";

function StatCard({ label, value, sub, icon, href }: { label: string; value: React.ReactNode; sub?: React.ReactNode; icon: React.ReactNode; href?: string }) {
  const body = (
    <div className="flex h-full items-start gap-3 rounded-lg border bg-card p-3 transition-colors hover:bg-muted/30">
      <div className="rounded-md bg-sky-500/10 p-2 text-sky-700 dark:text-sky-400">{icon}</div>
      <div className="min-w-0">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="text-xl font-semibold tabular-nums">{value}</div>
        {sub && <div className="mt-0.5 truncate text-xs text-muted-foreground">{sub}</div>}
      </div>
    </div>
  );
  return href ? (
    <Link href={href} className="rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
      {body}
    </Link>
  ) : (
    body
  );
}

const STATE_COLORS: Record<string, string> = {
  running: "#10b981",
  pending: "#f59e0b",
  stopping: "#f59e0b",
  rebooting: "#f59e0b",
  stopped: "#94a3b8",
  "shutting-down": "#f97316",
  terminated: "#cbd5e1",
};

function InstanceStates({ s }: { s: DashboardSummary }) {
  const rows = Object.entries(s.instances)
    .filter(([k]) => k !== "total")
    .map(([state, count]) => ({ state, count }));
  if (s.instances.total === 0) return <p className="py-8 text-center text-sm text-muted-foreground">No instances yet.</p>;
  return (
    <div role="img" aria-label={`Instance states: ${rows.map((r) => `${r.count} ${r.state}`).join(", ")}`} className="h-48">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={rows} layout="vertical" margin={{ left: 8, right: 16 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
          <XAxis type="number" allowDecimals={false} tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" />
          <YAxis type="category" dataKey="state" width={72} tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" />
          <Tooltip contentStyle={{ background: "var(--popover)", border: "1px solid var(--border)", borderRadius: 6, fontSize: 12 }} cursor={{ fill: "var(--muted)" }} />
          <Bar dataKey="count" isAnimationActive={false} radius={[0, 3, 3, 0]}>
            {rows.map((r) => (
              <Cell key={r.state} fill={STATE_COLORS[r.state] ?? "var(--chart-1)"} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

function Health({ s }: { s: DashboardSummary }) {
  const hz = useHealthz();
  return (
    <ul className="grid gap-2 text-sm">
      {(["backend", "engine", "database"] as const).map((k) => (
        <li key={k} className="flex items-center justify-between">
          <span className="capitalize">{k}</span>
          <StatusBadge status={s.health?.[k] ?? "unknown"} />
        </li>
      ))}
      <li className="flex items-center justify-between border-t pt-2 text-xs text-muted-foreground">
        <span>/healthz</span>
        {hz.isLoading ? "…" : hz.isError ? <StatusBadge status="unavailable" /> : <span>{hz.data?.status}{hz.data?.version ? ` · v${hz.data.version}` : ""}</span>}
      </li>
    </ul>
  );
}

export default function DashboardPage() {
  const q = useDashboardSummary();
  if (q.isLoading) return <LoadingState label="Loading dashboard…" />;
  if (q.isError || !q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const s = q.data;

  return (
    <div className="grid gap-4">
      <PageHeader title="Dashboard" description="Overview of your simulated account. Refreshes every 15 seconds and on live events." />

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Resources" value={formatNumber(s.resources_total)} icon={<Boxes className="size-4" />} sub="All simulated resources" />
        <StatCard
          label="Instances"
          value={formatNumber(s.instances.total)}
          icon={<Server className="size-4" />}
          sub={`${s.instances.running} running · ${s.instances.stopped} stopped · ${s.instances.pending} pending`}
          href="/ec2"
        />
        <StatCard
          label="S3 storage"
          value={formatBytes(s.storage_bytes)}
          icon={<HardDrive className="size-4" />}
          sub={`${formatNumber(s.buckets)} buckets · ${formatNumber(s.objects)} objects`}
          href="/s3"
        />
        <StatCard
          label="Alarms"
          value={s.alarms.alarm}
          icon={<Bell className="size-4" />}
          sub={`${s.alarms.ok} OK · ${s.alarms.insufficient_data} insufficient data`}
          href="/cloudwatch/alarms"
        />
        <StatCard label="VPCs" value={formatNumber(s.vpcs)} icon={<Network className="size-4" />} href="/vpc" />
        <StatCard label="Load balancers" value={formatNumber(s.load_balancers)} icon={<Scale className="size-4" />} href="/elb" />
        <StatCard label="Auto Scaling groups" value={formatNumber(s.autoscaling_groups)} icon={<Gauge className="size-4" />} href="/autoscaling" />
        <StatCard
          label="Average CPU"
          value={formatPercent(s.avg_cpu_percent)}
          icon={<Activity className="size-4" />}
          sub={`Net 5m: ${formatBytes(s.network_in_bytes_5m)} in · ${formatBytes(s.network_out_bytes_5m)} out`}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Section title="CPU utilization (all running instances)" className="lg:col-span-2">
          <MetricChart data={s.cpu_series ?? []} variant="area" yDomain={[0, 100]} unit="%" label="CPU" height={220} />
        </Section>
        <Section title="Instance states">
          <InstanceStates s={s} />
        </Section>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Section title="Storage">
          <dl className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">Buckets</dt>
              <dd className="text-lg font-semibold">{formatNumber(s.buckets)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Objects</dt>
              <dd className="text-lg font-semibold">{formatNumber(s.objects)}</dd>
            </div>
            <div className="col-span-2">
              <dt className="text-xs text-muted-foreground">Total size</dt>
              <dd className="text-lg font-semibold">{formatBytes(s.storage_bytes)}</dd>
            </div>
          </dl>
        </Section>
        <Section title="Alarms" actions={<Link href="/cloudwatch/alarms" className="text-xs underline">View alarms</Link>}>
          <ul className="grid gap-2 text-sm">
            <li className="flex justify-between"><StatusBadge status="ALARM" label="In alarm" /><span className="font-semibold">{s.alarms.alarm}</span></li>
            <li className="flex justify-between"><StatusBadge status="OK" /><span className="font-semibold">{s.alarms.ok}</span></li>
            <li className="flex justify-between"><StatusBadge status="INSUFFICIENT_DATA" label="Insufficient data" /><span className="font-semibold">{s.alarms.insufficient_data}</span></li>
          </ul>
        </Section>
        <Section title="Service health">
          <Health s={s} />
        </Section>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Section title="Recent events">
          {(s.recent_events ?? []).length === 0 ? (
            <p className="text-sm text-muted-foreground">No events yet.</p>
          ) : (
            <ul className="grid gap-1.5 text-sm" aria-label="Recent events">
              {(s.recent_events ?? []).map((e) => (
                <li key={e.id} className="flex items-baseline gap-2 border-b pb-1.5 last:border-b-0">
                  <span className="w-16 shrink-0 text-xs text-muted-foreground">{formatTime(e.ts)}</span>
                  <span className="font-mono text-xs">{e.resource_id}</span>
                  <span className="truncate text-muted-foreground">{e.message}</span>
                </li>
              ))}
            </ul>
          )}
        </Section>
        <Section title="Active labs" actions={<Link href="/labs" className="text-xs underline">All labs</Link>}>
          {(s.active_labs ?? []).length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No labs in progress. <Link href="/labs" className="underline">Start a training lab</Link>.
            </p>
          ) : (
            <ul className="grid gap-3">
              {(s.active_labs ?? []).map((l) => {
                const pct = l.total_steps ? Math.round((l.completed_steps / l.total_steps) * 100) : 0;
                return (
                  <li key={l.lab_id}>
                    <Link href={`/labs/${l.lab_id}`} className="text-sm font-medium hover:underline">
                      {l.title}
                    </Link>
                    <div className="mt-1 flex items-center gap-2">
                      <Progress value={pct} aria-label={`${l.title} progress`} className="h-1.5" />
                      <span className="shrink-0 text-xs text-muted-foreground">
                        {l.completed_steps}/{l.total_steps}
                      </span>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </Section>
      </div>

      <Section title="Recent activity (audit log)">
        {(s.recent_activity ?? []).length === 0 ? (
          <p className="text-sm text-muted-foreground">No API activity recorded yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm" aria-label="Recent activity">
              <thead>
                <tr className="border-b text-left text-xs text-muted-foreground">
                  <th className="py-1.5 pr-3 font-semibold">Time</th>
                  <th className="py-1.5 pr-3 font-semibold">Principal</th>
                  <th className="py-1.5 pr-3 font-semibold">Action</th>
                  <th className="py-1.5 pr-3 font-semibold">Resource</th>
                  <th className="py-1.5 pr-3 font-semibold">Result</th>
                  <th className="py-1.5 font-semibold">Status</th>
                </tr>
              </thead>
              <tbody>
                {(s.recent_activity ?? []).map((a) => (
                  <tr key={a.id} className="border-b last:border-b-0">
                    <td className="py-1.5 pr-3 text-xs whitespace-nowrap text-muted-foreground" title={a.ts}>{relativeTime(a.ts)}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs">{a.principal}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs">{a.action}</td>
                    <td className="max-w-72 truncate py-1.5 pr-3 font-mono text-xs" title={a.resource}>{a.resource}</td>
                    <td className="py-1.5 pr-3"><StatusBadge status={a.result} /></td>
                    <td className="py-1.5 font-mono text-xs">{a.status_code}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Section>
    </div>
  );
}
