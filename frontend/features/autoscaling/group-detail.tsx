"use client";

import Link from "next/link";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, Play, Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { MetricChart } from "@/components/console/metric-chart";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { StatusBadge } from "@/components/console/status-badge";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { AutoScalingGroup, ScalingActivity, ScalingPolicy } from "@/lib/types";
import { useMetricData } from "@/features/cloudwatch/metrics-api";
import { useActivities, useCreatePolicy, useDeleteGroup, useDeletePolicy, useEvaluate, useGroup, usePolicies, useUpdateCapacity, type EvaluateResult } from "./api";
import { inService, validateCapacity } from "./groups";

/** Dimension name used for CloudSim/AutoScaling metrics (the API doc does not name it; AWS uses AutoScalingGroupName). */
export const ASG_DIMENSION = "AutoScalingGroupName";

export function GroupDetail({ name }: { name: string }) {
  const q = useGroup(name);
  const del = useDeleteGroup();
  const evaluate = useEvaluate(name);
  const router = useRouter();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [force, setForce] = useState(false);
  const [evalResult, setEvalResult] = useState<EvaluateResult | null>(null);

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const grp = q.data;

  const runEvaluate = async () => {
    try {
      const r = await evaluate.mutateAsync();
      setEvalResult(r ?? {});
      toast.success(r?.reason ? `Evaluation: ${r.reason}` : "Scaling evaluation completed");
    } catch {
      // toast shown by mutation cache
    }
  };

  return (
    <div className="grid gap-4">
      <PageHeader
        title={grp.name}
        description={<Mono copy>{grp.arn}</Mono>}
        breadcrumbs={[{ label: "Auto Scaling", href: "/autoscaling" }, { label: grp.name }]}
        actions={
          <>
            <Button size="sm" variant="outline" onClick={() => void runEvaluate()} disabled={evaluate.isPending}>
              {evaluate.isPending ? <Loader2 className="animate-spin" /> : <Play />} Evaluate now
            </Button>
            <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
              <Trash2 /> Delete
            </Button>
          </>
        }
      />
      {evalResult && (
        <div role="status" className="rounded-md border border-sky-500/30 bg-sky-500/5 p-3 text-sm" data-testid="evaluate-result">
          <div className="font-medium">Scaling evaluation result</div>
          <div className="mt-1 text-muted-foreground">{evalResult.reason ?? "No reason returned."}</div>
          <div className="mt-1 flex flex-wrap gap-4 text-xs">
            {evalResult.desired !== undefined && <span>Desired: {evalResult.desired}</span>}
            {evalResult.change !== undefined && <span>Change: {evalResult.change > 0 ? `+${evalResult.change}` : evalResult.change}</span>}
            {evalResult.replace && evalResult.replace.length > 0 && <span>Replace: {evalResult.replace.join(", ")}</span>}
          </div>
        </div>
      )}
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "Status", value: grp.status ? <StatusBadge status={grp.status} /> : "—" },
            { label: "Launch template", value: grp.launch_template },
            { label: "In service", value: `${inService(grp)} of ${grp.desired_capacity} desired` },
            { label: "Subnets", value: <span className="font-mono text-xs">{(grp.subnet_ids ?? []).join(", ")}</span> },
            { label: "Target groups", value: <span className="font-mono text-xs">{(grp.target_group_ids ?? []).join(", ") || "—"}</span> },
            { label: "Health check type", value: grp.health_check_type ?? "EC2" },
            { label: "Cooldown", value: grp.cooldown_s !== undefined ? `${grp.cooldown_s}s` : "—" },
            { label: "Created", value: formatDate(grp.created_at) },
          ]}
        />
      </Section>
      <CapacityEditor grp={grp} />
      <Section title="Instances">
        <ResourceTable
          label="Group instances"
          globalSearch={false}
          getRowId={(i) => i.instance_id}
          data={grp.instances ?? []}
          columns={[
            {
              id: "instance_id",
              header: "Instance",
              sortable: true,
              cell: (i) => (
                <Link className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400" href={`/ec2/instances/${encodeURIComponent(i.instance_id)}`}>
                  {i.instance_id}
                </Link>
              ),
            },
            { id: "state", header: "State", sortable: true, cell: (i) => <StatusBadge status={i.state} /> },
            { id: "health", header: "Health", sortable: true, cell: (i) => <StatusBadge status={i.health} /> },
          ]}
          emptyTitle="No instances"
          emptyDescription="The group launches instances to reach its desired capacity."
        />
      </Section>
      <CapacityChart name={grp.name} />
      <PoliciesSection name={grp.name} />
      <ActivitiesSection name={grp.name} />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={(o) => {
          setConfirmDelete(o);
          if (!o) setForce(false);
        }}
        title={`Delete Auto Scaling group ${grp.name}?`}
        description="Without force, deletion fails while the group still has instances."
        confirmLabel="Delete"
        destructive
        typeToConfirm={grp.name}
        onConfirm={async () => {
          await del.mutateAsync({ name: grp.name, force });
          toast.success(`Group ${grp.name} deleted`);
          router.push("/autoscaling");
        }}
      >
        <div className="flex items-center gap-2">
          <Checkbox id="asg-detail-force" checked={force} onCheckedChange={(c) => setForce(c === true)} />
          <Label htmlFor="asg-detail-force" className="text-sm font-normal">
            Force delete – terminate the group&apos;s instances
          </Label>
        </div>
      </ConfirmDialog>
    </div>
  );
}

function CapacityEditor({ grp }: { grp: AutoScalingGroup }) {
  const update = useUpdateCapacity(grp.name);
  const [f, setF] = useState({ min: String(grp.min_size), max: String(grp.max_size), desired: String(grp.desired_capacity) });
  const [base, setBase] = useState(grp);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);
  // Re-sync the form when the server values change (e.g. after a scaling activity).
  if (base.min_size !== grp.min_size || base.max_size !== grp.max_size || base.desired_capacity !== grp.desired_capacity) {
    setBase(grp);
    setF({ min: String(grp.min_size), max: String(grp.max_size), desired: String(grp.desired_capacity) });
  }
  const dirty = f.min !== String(grp.min_size) || f.max !== String(grp.max_size) || f.desired !== String(grp.desired_capacity);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validateCapacity(f.min, f.max, f.desired);
    setErrors(errs);
    if (Object.keys(errs).length) return;
    setErr(null);
    try {
      await update.mutateAsync({ min_size: Number(f.min), max_size: Number(f.max), desired_capacity: Number(f.desired) });
      toast.success("Capacity updated");
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  };

  return (
    <Section title="Group size">
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-[repeat(3,minmax(0,10rem))_auto] sm:items-start" noValidate>
        <FormField label="Minimum capacity" error={errors.min_size}>
          {(p) => <Input {...p} type="number" min={0} value={f.min} onChange={(e) => setF((s) => ({ ...s, min: e.target.value }))} />}
        </FormField>
        <FormField label="Desired capacity" error={errors.desired_capacity}>
          {(p) => <Input {...p} type="number" min={0} value={f.desired} onChange={(e) => setF((s) => ({ ...s, desired: e.target.value }))} />}
        </FormField>
        <FormField label="Maximum capacity" error={errors.max_size}>
          {(p) => <Input {...p} type="number" min={0} value={f.max} onChange={(e) => setF((s) => ({ ...s, max: e.target.value }))} />}
        </FormField>
        <Button type="submit" className="sm:mt-5" disabled={!dirty || update.isPending}>
          {update.isPending ? <Loader2 className="animate-spin" /> : <Save />} Update capacity
        </Button>
      </form>
      <div className="mt-2">
        <FormError message={err} />
      </div>
    </Section>
  );
}

function CapacityChart({ name }: { name: string }) {
  const dimension = `${ASG_DIMENSION}:${name}`;
  const desired = useMetricData({ namespace: "CloudSim/AutoScaling", metric_name: "GroupDesiredCapacity", dimension, rangeMinutes: 60, period: 60, stat: "Maximum" });
  const inSvc = useMetricData({ namespace: "CloudSim/AutoScaling", metric_name: "GroupInServiceInstances", dimension, rangeMinutes: 60, period: 60, stat: "Average" });
  return (
    <Section title="Capacity (last hour)">
      <div className="grid gap-4 md:grid-cols-2">
        <div>
          <h3 className="mb-1 text-xs font-medium text-muted-foreground">GroupDesiredCapacity</h3>
          {desired.isError ? <ErrorState compact error={desired.error} /> : <MetricChart data={desired.data?.datapoints ?? []} height={180} label="Desired" formatValue={(v) => v.toFixed(0)} />}
        </div>
        <div>
          <h3 className="mb-1 text-xs font-medium text-muted-foreground">GroupInServiceInstances</h3>
          {inSvc.isError ? (
            <ErrorState compact error={inSvc.error} />
          ) : (
            <MetricChart data={inSvc.data?.datapoints ?? []} height={180} label="In service" color="var(--chart-2)" formatValue={(v) => v.toFixed(0)} />
          )}
        </div>
      </div>
    </Section>
  );
}

function policyDescription(p: ScalingPolicy) {
  return p.type === "target_tracking"
    ? `Keep average CPU at ${p.target_value}%`
    : `When CPU ${p.comparison} ${p.threshold}%, adjust by ${p.adjustment > 0 ? "+" : ""}${p.adjustment}`;
}

function PoliciesSection({ name }: { name: string }) {
  const q = usePolicies(name);
  const create = useCreatePolicy(name);
  const del = useDeletePolicy(name);
  const [open, setOpen] = useState(false);
  const [toDelete, setToDelete] = useState<ScalingPolicy | null>(null);
  const [f, setF] = useState({ name: "", type: "target_tracking", target_value: "50", threshold: "80", comparison: ">=", adjustment: "1" });
  const [err, setErr] = useState<string | null>(null);
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF((s) => ({ ...s, [k]: e.target.value }));

  const columns: Column<ScalingPolicy>[] = [
    { id: "name", header: "Name", sortable: true, cell: (p) => <span className="font-medium">{p.name}</span> },
    { id: "type", header: "Type", sortable: true, cell: (p) => (p.type === "target_tracking" ? "Target tracking" : "Step") },
    { id: "desc", header: "Rule", cell: (p) => policyDescription(p) },
    {
      id: "actions",
      header: "",
      cell: (p) => (
        <Button size="icon-sm" variant="ghost" aria-label={`Delete policy ${p.name}`} onClick={() => setToDelete(p)}>
          <Trash2 />
        </Button>
      ),
    },
  ];

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!/^[a-zA-Z0-9_.-]{1,255}$/.test(f.name)) return setErr("Policy name: 1–255 letters, digits, '_', '.', '-'.");
    let body: ScalingPolicy;
    if (f.type === "target_tracking") {
      const t = Number(f.target_value);
      if (!(t > 0 && t <= 100)) return setErr("Target value must be between 0 and 100 (CPU %).");
      body = { name: f.name, type: "target_tracking", target_value: t };
    } else {
      const th = Number(f.threshold);
      const adj = Number(f.adjustment);
      if (!(th >= 0 && th <= 100)) return setErr("Threshold must be 0–100 (CPU %).");
      if (!Number.isInteger(adj) || adj === 0) return setErr("Adjustment must be a non-zero whole number.");
      body = { name: f.name, type: "step", threshold: th, comparison: f.comparison, adjustment: adj };
    }
    setErr(null);
    try {
      await create.mutateAsync(body);
      toast.success(`Policy ${f.name} created`);
      setOpen(false);
      setF((s) => ({ ...s, name: "" }));
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  };

  return (
    <Section title="Scaling policies">
      <ResourceTable
        label="Scaling policies"
        globalSearch={false}
        hideSearch
        columns={columns}
        getRowId={(p) => p.name}
        data={q.data}
        isLoading={q.isLoading}
        error={q.error}
        onRetry={() => void q.refetch()}
        toolbar={
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus /> Add policy
          </Button>
        }
        emptyTitle="No scaling policies"
        emptyDescription="Without policies the group only maintains its desired capacity."
      />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add scaling policy</DialogTitle>
          </DialogHeader>
          <form onSubmit={submit} className="grid gap-3" noValidate>
            <FormError message={err} />
            <FormField label="Policy name" required>
              {(p) => <Input {...p} value={f.name} onChange={set("name")} placeholder="cpu50" />}
            </FormField>
            <FormField label="Policy type">
              {(p) => (
                <NativeSelect {...p} value={f.type} onChange={set("type")}>
                  <option value="target_tracking">Target tracking (average CPU)</option>
                  <option value="step">Step scaling (CPU threshold)</option>
                </NativeSelect>
              )}
            </FormField>
            {f.type === "target_tracking" ? (
              <FormField label="Target average CPU (%)" hint="desired = ceil(running × avg CPU ÷ target); scale-in below 80% of target.">
                {(p) => <Input {...p} type="number" value={f.target_value} onChange={set("target_value")} />}
              </FormField>
            ) : (
              <div className="grid grid-cols-3 gap-3">
                <FormField label="Comparison">
                  {(p) => (
                    <NativeSelect {...p} value={f.comparison} onChange={set("comparison")}>
                      {[">=", ">", "<=", "<"].map((c) => (
                        <option key={c} value={c}>
                          {c}
                        </option>
                      ))}
                    </NativeSelect>
                  )}
                </FormField>
                <FormField label="Threshold (CPU %)">{(p) => <Input {...p} type="number" value={f.threshold} onChange={set("threshold")} />}</FormField>
                <FormField label="Adjustment">{(p) => <Input {...p} type="number" value={f.adjustment} onChange={set("adjustment")} />}</FormField>
              </div>
            )}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={create.isPending}>
                {create.isPending && <Loader2 className="animate-spin" />} Add policy
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete policy ${toDelete?.name}?`}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          if (toDelete) await del.mutateAsync(toDelete.name);
          toast.success("Policy deleted");
        }}
      />
    </Section>
  );
}

function ActivitiesSection({ name }: { name: string }) {
  const q = useActivities(name);
  const columns: Column<ScalingActivity>[] = [
    { id: "ts", header: "Time", sortable: true, cell: (a) => <span className="whitespace-nowrap">{formatDate(a.ts)}</span> },
    { id: "status", header: "Status", sortable: true, cell: (a) => <StatusBadge status={a.status} /> },
    { id: "description", header: "Description", cell: (a) => a.description },
    { id: "cause", header: "Cause", cell: (a) => <span className="text-xs text-muted-foreground">{a.cause}</span> },
  ];
  return (
    <Section title="Activity history">
      <ResourceTable
        label="Scaling activities"
        globalSearch={false}
        columns={columns}
        getRowId={(a) => String(a.id)}
        data={q.data ? [...q.data].sort((a, b) => b.ts.localeCompare(a.ts)) : undefined}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        emptyTitle="No scaling activity yet"
        emptyDescription="Launches, terminations and replacements appear here (refreshes every 10 s)."
      />
    </Section>
  );
}
