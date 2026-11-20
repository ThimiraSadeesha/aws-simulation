"use client";

import Link from "next/link";
import { useState } from "react";
import { Loader2, RefreshCw, Save } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormField, NativeSelect } from "@/components/console/form-field";
import { KeyValueEditor, kvFromRecord, kvToRecord, type KV } from "@/components/console/key-value-editor";
import { MetricChart } from "@/components/console/metric-chart";
import { ResourceTable } from "@/components/console/resource-table";
import { ErrorState, LoadingState, QueryBoundary } from "@/components/console/states";
import { Mono } from "@/components/console/copy-button";
import { StatusBadge } from "@/components/console/status-badge";
import { TIME_RANGES, useMetricData } from "@/features/cloudwatch/metrics-api";
import { formatBytes, formatDate } from "@/lib/format";
import { validateTags } from "@/lib/validation";
import type { CloudEvent, Instance, Statistic, WorkloadProfile } from "@/lib/types";
import {
  useConsoleOutput,
  useInstanceHistory,
  useInstanceMetadata,
  useUpdateInstanceSecurityGroups,
  useUpdateInstanceTags,
  useUpdateWorkload,
} from "./api";
import { useSecurityGroupOptions } from "./network-api";

export function DetailsTab({ instance: i }: { instance: Instance }) {
  return (
    <Section title="Instance summary">
      <DetailGrid
        items={[
          { label: "Instance ID", value: <Mono copy>{i.instance_id}</Mono> },
          { label: "Name", value: i.name || i.tags?.Name },
          { label: "State", value: <StatusBadge status={i.state} /> },
          { label: "State reason", value: i.state_reason || undefined },
          { label: "Instance type", value: <span className="font-mono text-xs">{i.instance_type}</span> },
          { label: "vCPU / Memory", value: `${i.vcpu} vCPU · ${i.memory_mib} MiB` },
          { label: "Image (AMI)", value: <Mono copy>{i.image_id}</Mono> },
          { label: "Key pair", value: i.key_name || undefined },
          { label: "Workload profile", value: i.workload_profile },
          { label: "Auto Scaling group", value: i.autoscaling_group || undefined },
          { label: "Launch time", value: formatDate(i.launch_time) },
          { label: "Last updated", value: formatDate(i.updated_at) },
        ]}
      />
    </Section>
  );
}

const METRICS: { name: string; label: string; unit?: string; bytes?: boolean; percent?: boolean; color: string }[] = [
  { name: "CPUUtilization", label: "CPU utilization (%)", percent: true, color: "var(--chart-1)" },
  { name: "MemoryUtilization", label: "Memory utilization (%)", percent: true, color: "var(--chart-2)" },
  { name: "NetworkIn", label: "Network in (bytes)", bytes: true, color: "var(--chart-3)" },
  { name: "NetworkOut", label: "Network out (bytes)", bytes: true, color: "var(--chart-4)" },
];

function MetricPanel({ instanceId, metric, range, stat, period }: { instanceId: string; metric: (typeof METRICS)[number]; range: number; stat: Statistic; period: number }) {
  const q = useMetricData({
    namespace: "CloudSim/EC2",
    metric_name: metric.name,
    dimension: `InstanceId:${instanceId}`,
    rangeMinutes: range,
    period,
    stat,
  });
  return (
    <div className="rounded-lg border bg-card p-3">
      <h3 className="mb-2 text-xs font-semibold">{metric.label}</h3>
      {q.isLoading ? (
        <LoadingState />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} compact />
      ) : (
        <MetricChart
          data={q.data?.datapoints ?? []}
          label={metric.label}
          color={metric.color}
          height={180}
          yDomain={metric.percent ? [0, 100] : undefined}
          formatValue={metric.bytes ? (v) => formatBytes(v, 0) : undefined}
        />
      )}
    </div>
  );
}

const PROFILES: WorkloadProfile[] = ["idle", "steady", "spiky", "stress", "custom"];

export function MonitoringTab({ instance }: { instance: Instance }) {
  const [range, setRange] = useState(60);
  const [stat, setStat] = useState<Statistic>("Average");
  const [period, setPeriod] = useState(60);
  const [profile, setProfile] = useState<WorkloadProfile>(instance.workload_profile || "steady");
  const [cpuTarget, setCpuTarget] = useState("70");
  const workload = useUpdateWorkload(instance.instance_id);
  const targetNum = Number(cpuTarget);
  const targetInvalid = profile === "custom" && (!Number.isFinite(targetNum) || targetNum < 0 || targetNum > 100);

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-end gap-3 rounded-lg border bg-card p-3">
        <FormField label="Time range" className="w-36">
          {(p) => (
            <NativeSelect {...p} value={range} onChange={(e) => setRange(Number(e.target.value))}>
              {TIME_RANGES.map((r) => (
                <option key={r.minutes} value={r.minutes}>
                  {r.label}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Statistic" className="w-32">
          {(p) => (
            <NativeSelect {...p} value={stat} onChange={(e) => setStat(e.target.value as Statistic)}>
              {(["Average", "Maximum", "Minimum", "Sum", "SampleCount"] as const).map((s) => (
                <option key={s}>{s}</option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Period" className="w-28">
          {(p) => (
            <NativeSelect {...p} value={period} onChange={(e) => setPeriod(Number(e.target.value))}>
              {[60, 300, 900, 3600].map((s) => (
                <option key={s} value={s}>
                  {s < 3600 ? `${s / 60} min` : "1 hour"}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <div className="ml-auto flex flex-wrap items-end gap-2">
          <FormField label="Workload profile" className="w-32" hint={`Current: ${instance.workload_profile}`}>
            {(p) => (
              <NativeSelect {...p} value={profile} onChange={(e) => setProfile(e.target.value as WorkloadProfile)}>
                {PROFILES.map((s) => (
                  <option key={s}>{s}</option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          {profile === "custom" && (
            <FormField label="CPU target %" className="w-28" error={targetInvalid ? "0–100" : null}>
              {(p) => <Input {...p} type="number" min={0} max={100} value={cpuTarget} onChange={(e) => setCpuTarget(e.target.value)} />}
            </FormField>
          )}
          <Button
            size="sm"
            disabled={workload.isPending || targetInvalid}
            onClick={() =>
              workload.mutate(profile === "custom" ? { profile, cpu_target: targetNum } : { profile }, {
                onSuccess: () => toast.success(`Workload profile set to ${profile}`),
              })
            }
          >
            {workload.isPending && <Loader2 className="animate-spin" />} Apply workload
          </Button>
        </div>
      </div>
      {instance.state !== "running" && (
        <p className="text-xs text-muted-foreground">Non-running instances produce no datapoints.</p>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        {METRICS.map((m) => (
          <MetricPanel key={m.name} instanceId={instance.instance_id} metric={m} range={range} stat={stat} period={period} />
        ))}
      </div>
    </div>
  );
}

export function SecurityTab({ instance }: { instance: Instance }) {
  const sgs = useSecurityGroupOptions(instance.vpc_id || undefined);
  const update = useUpdateInstanceSecurityGroups(instance.instance_id);
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState<string[]>(instance.security_group_ids ?? []);
  const attached = (sgs.data ?? []).filter((g) => instance.security_group_ids?.includes(g.group_id));

  return (
    <Section
      title="Security groups"
      actions={
        !editing ? (
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setSelected(instance.security_group_ids ?? []);
              setEditing(true);
            }}
          >
            Change security groups
          </Button>
        ) : null
      }
    >
      {editing ? (
        <div className="grid gap-3">
          {sgs.isLoading ? (
            <LoadingState />
          ) : (
            <ul className="grid gap-1 sm:grid-cols-2">
              {(sgs.data ?? []).map((g) => (
                <li key={g.group_id} className="flex items-center gap-2 rounded-md border px-2 py-1.5">
                  <Checkbox
                    id={`edit-sg-${g.group_id}`}
                    checked={selected.includes(g.group_id)}
                    onCheckedChange={(c) => setSelected((s) => (c ? [...s, g.group_id] : s.filter((x) => x !== g.group_id)))}
                  />
                  <Label htmlFor={`edit-sg-${g.group_id}`} className="text-sm font-normal">
                    {g.group_name} <span className="font-mono text-xs text-muted-foreground">{g.group_id}</span>
                  </Label>
                </li>
              ))}
            </ul>
          )}
          {selected.length === 0 && <p className="text-xs text-destructive">Select at least one security group.</p>}
          <div className="flex gap-2">
            <Button
              size="sm"
              disabled={update.isPending || selected.length === 0}
              onClick={() =>
                update.mutate(selected, {
                  onSuccess: () => {
                    toast.success("Security groups updated");
                    setEditing(false);
                  },
                })
              }
            >
              {update.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save
            </Button>
            <Button size="sm" variant="outline" onClick={() => setEditing(false)}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (instance.security_group_ids ?? []).length === 0 ? (
        <p className="text-sm text-muted-foreground">No security groups attached.</p>
      ) : (
        <div className="grid gap-4">
          {(instance.security_group_ids ?? []).map((id) => {
            const g = attached.find((x) => x.group_id === id);
            return (
              <div key={id} className="rounded-md border">
                <div className="flex items-center gap-2 border-b bg-muted/40 px-3 py-1.5 text-sm">
                  <Link href={`/vpc/security-groups/${id}`} className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400">
                    {id}
                  </Link>
                  <span className="font-medium">{g?.group_name}</span>
                </div>
                {g && (
                  <div className="grid gap-2 p-3 text-xs md:grid-cols-2">
                    {(["ingress", "egress"] as const).map((dir) => (
                      <div key={dir}>
                        <div className="mb-1 font-semibold capitalize">{dir} rules</div>
                        {g[dir].length === 0 ? (
                          <p className="text-muted-foreground">None</p>
                        ) : (
                          <ul className="grid gap-0.5 font-mono">
                            {g[dir].map((r) => (
                              <li key={r.rule_id}>
                                {r.protocol === "-1" ? "all" : r.protocol} {r.protocol === "-1" ? "" : `${r.from_port}-${r.to_port}`}{" "}
                                {r.cidr || r.source_security_group_id}
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </Section>
  );
}

export function NetworkingTab({ instance: i }: { instance: Instance }) {
  return (
    <Section
      title="Networking"
      actions={
        i.vpc_id ? (
          <Button asChild size="sm" variant="outline">
            <Link href={`/vpc/vpcs/${i.vpc_id}`}>View VPC topology</Link>
          </Button>
        ) : null
      }
    >
      <DetailGrid
        items={[
          { label: "VPC", value: i.vpc_id ? <Link className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400" href={`/vpc/vpcs/${i.vpc_id}`}>{i.vpc_id}</Link> : undefined },
          { label: "Subnet", value: <Mono copy>{i.subnet_id}</Mono> },
          { label: "Private IPv4", value: <Mono copy>{i.private_ip}</Mono> },
          { label: "Public IPv4", value: <Mono copy>{i.public_ip}</Mono> },
          { label: "Private DNS", value: <Mono copy>{i.private_dns}</Mono> },
          { label: "Availability zone", value: "local-1a" },
        ]}
      />
    </Section>
  );
}

export function TagsTab({ instance }: { instance: Instance }) {
  const [rows, setRows] = useState<KV[]>(() => kvFromRecord(instance.tags));
  const update = useUpdateInstanceTags(instance.instance_id);
  const err = validateTags(rows);
  return (
    <Section
      title="Tags"
      actions={
        <Button
          size="sm"
          disabled={!!err || update.isPending}
          onClick={() => update.mutate(kvToRecord(rows), { onSuccess: () => toast.success("Tags saved") })}
        >
          {update.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save tags
        </Button>
      }
    >
      <p className="mb-3 text-xs text-muted-foreground">Saving replaces all tags on the instance.</p>
      <KeyValueEditor value={rows} onChange={setRows} />
      {err && (
        <p role="alert" className="mt-2 text-xs text-destructive">
          {err}
        </p>
      )}
    </Section>
  );
}

export function ConsoleTab({ instanceId }: { instanceId: string }) {
  const q = useConsoleOutput(instanceId);
  return (
    <Section
      title="Console output"
      actions={
        <Button size="sm" variant="outline" onClick={() => void q.refetch()} disabled={q.isFetching}>
          {q.isFetching ? <Loader2 className="animate-spin" /> : <RefreshCw />} Refresh
        </Button>
      }
    >
      <QueryBoundary query={q}>
        {(d) => (
          <div className="grid gap-2">
            <p className="text-xs text-muted-foreground">Captured {formatDate(d.ts)} · user data is parsed and echoed, not executed.</p>
            <pre className="max-h-[480px] overflow-auto rounded-md bg-zinc-950 p-3 font-mono text-xs leading-relaxed text-zinc-100" aria-label="Console output">
              {d.output || "(empty)"}
            </pre>
          </div>
        )}
      </QueryBoundary>
    </Section>
  );
}

export function MetadataTab({ instanceId }: { instanceId: string }) {
  const q = useInstanceMetadata(instanceId);
  return (
    <Section title="Instance metadata (IMDS)">
      <QueryBoundary query={q}>
        {(d) => (
          <table className="w-full text-sm" aria-label="Instance metadata">
            <thead>
              <tr className="border-b text-left text-xs text-muted-foreground">
                <th className="py-1.5 pr-4 font-semibold">Path</th>
                <th className="py-1.5 font-semibold">Value</th>
              </tr>
            </thead>
            <tbody>
              {Object.entries(d).map(([k, v]) => (
                <tr key={k} className="border-b last:border-b-0">
                  <td className="py-1.5 pr-4 font-mono text-xs">{k}</td>
                  <td className="py-1.5 font-mono text-xs break-all">{typeof v === "string" ? v : JSON.stringify(v)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </QueryBoundary>
    </Section>
  );
}

export function HistoryTab({ instanceId }: { instanceId: string }) {
  const q = useInstanceHistory(instanceId);
  return (
    <ResourceTable<CloudEvent>
      label="State history"
      data={q.data}
      isLoading={q.isLoading}
      isFetching={q.isFetching}
      error={q.error}
      onRetry={() => void q.refetch()}
      getRowId={(e) => String(e.id)}
      globalSearch={false}
      columns={[
        { id: "ts", header: "Time", sortable: true, cell: (e) => <span className="text-xs">{formatDate(e.ts)}</span> },
        { id: "type", header: "Event", sortable: true, cell: (e) => <span className="font-mono text-xs">{e.type}</span> },
        { id: "message", header: "Message", cell: (e) => e.message },
      ]}
      emptyTitle="No state changes recorded"
    />
  );
}
