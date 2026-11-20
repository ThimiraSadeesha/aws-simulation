"use client";

import Link from "next/link";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { StatusBadge } from "@/components/console/status-badge";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import type { Target, TargetGroup } from "@/lib/types";
import {
  useCreateTargetGroup,
  useDeleteTargetGroup,
  useDeregisterTarget,
  useRegisterTargets,
  useTargetGroup,
  useTargetGroups,
  useTargetHealth,
  useUpdateTargetGroup,
} from "./api";
import { CheckboxList } from "./checkbox-list";
import { useInstanceOptions, useVpcOptions } from "./lookups";

const ALGORITHMS: { value: TargetGroup["algorithm"]; label: string }[] = [
  { value: "round_robin", label: "Round robin" },
  { value: "least_outstanding_requests", label: "Least outstanding requests" },
  { value: "weighted_random", label: "Weighted random" },
];

function healthSummary(tg: TargetGroup) {
  const t = tg.targets ?? [];
  const healthy = t.filter((x) => x.health === "healthy").length;
  return `${healthy}/${t.length} healthy`;
}

export function TargetGroupsPage() {
  const list = useListParams({ sort: "name" });
  const q = useTargetGroups(list.params);
  const del = useDeleteTargetGroup();
  const router = useRouter();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<TargetGroup[]>([]);

  const columns: Column<TargetGroup>[] = [
    {
      id: "name",
      header: "Name",
      sortable: true,
      cell: (r) => (
        <Link href={`/elb/target-groups/${encodeURIComponent(r.target_group_id)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {r.name}
        </Link>
      ),
    },
    { id: "target_group_id", header: "ID", sortable: true, cell: (r) => <Mono>{r.target_group_id}</Mono> },
    { id: "protocol", header: "Protocol : Port", cell: (r) => `${r.protocol}:${r.port}` },
    { id: "vpc_id", header: "VPC", sortable: true, cell: (r) => <Mono>{r.vpc_id}</Mono> },
    { id: "algorithm", header: "Algorithm", sortable: true, cell: (r) => ALGORITHMS.find((a) => a.value === r.algorithm)?.label ?? r.algorithm },
    { id: "targets", header: "Targets", cell: (r) => healthSummary(r) },
  ];

  return (
    <div>
      <PageHeader
        title="Target groups"
        description="Groups of instances that receive traffic from load balancer listeners, with simulated health checks."
        breadcrumbs={[{ label: "Load Balancers", href: "/elb" }, { label: "Target groups" }]}
      />
      <ResourceTable
        label="Target groups"
        columns={columns}
        getRowId={(r) => r.target_group_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: (rows) => setToDelete(rows) }]}
        onRowClick={(r) => router.push(`/elb/target-groups/${encodeURIComponent(r.target_group_id)}`)}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create target group
          </Button>
        }
        emptyTitle="No target groups"
        emptyDescription="Create a target group and register instances in it."
      />
      <CreateTargetGroupDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length} target group(s)?`}
        description={<span className="font-mono text-xs">{toDelete.map((t) => t.name).join(", ")}</span>}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const tg of toDelete) await del.mutateAsync(tg.target_group_id);
          toast.success(`Deleted ${toDelete.length} target group(s)`);
        }}
      />
    </div>
  );
}

function CreateTargetGroupDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const vpcs = useVpcOptions();
  const create = useCreateTargetGroup();
  const router = useRouter();
  const initial = { name: "", port: "80", vpc_id: "", path: "/", interval: "30", healthy: "2", unhealthy: "2" };
  const [f, setF] = useState(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF((s) => ({ ...s, [k]: e.target.value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    if (!/^[a-zA-Z0-9-]{1,32}$/.test(f.name)) errs.name = "1–32 characters: letters, digits and hyphens.";
    const port = Number(f.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) errs.port = "Port must be 1–65535.";
    if (!f.vpc_id) errs.vpc_id = "Choose a VPC.";
    if (!f.path.startsWith("/")) errs.path = "Health check path must start with '/'.";
    const interval = Number(f.interval);
    if (!Number.isInteger(interval) || interval < 5 || interval > 300) errs.interval = "Interval must be 5–300 seconds.";
    const h = Number(f.healthy);
    const u = Number(f.unhealthy);
    if (!Number.isInteger(h) || h < 2 || h > 10) errs.healthy = "2–10.";
    if (!Number.isInteger(u) || u < 2 || u > 10) errs.unhealthy = "2–10.";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    const tg = await create
      .mutateAsync({
        name: f.name,
        protocol: "HTTP",
        port,
        vpc_id: f.vpc_id,
        health_check: { path: f.path, interval_s: interval, healthy_threshold: h, unhealthy_threshold: u },
      })
      .catch(() => null);
    if (tg) {
      toast.success(`Target group ${tg.name} created`);
      onOpenChange(false);
      setF(initial);
      if (tg.target_group_id) router.push(`/elb/target-groups/${encodeURIComponent(tg.target_group_id)}`);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          setErrors({});
          create.reset();
        }
        onOpenChange(o);
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Create target group</DialogTitle>
          <DialogDescription>Instances registered here must be in the same VPC.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="grid gap-3" noValidate>
          <FormError message={create.error ? errorMessage(create.error) : null} />
          <FormField label="Name" required error={errors.name}>
            {(p) => <Input {...p} value={f.name} onChange={set("name")} placeholder="web-targets" />}
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <FormField label="Protocol">
              {(p) => (
                <NativeSelect {...p} value="HTTP" disabled>
                  <option>HTTP</option>
                </NativeSelect>
              )}
            </FormField>
            <FormField label="Port" required error={errors.port}>
              {(p) => <Input {...p} type="number" value={f.port} onChange={set("port")} />}
            </FormField>
          </div>
          <FormField label="VPC" required error={errors.vpc_id}>
            {(p) => (
              <NativeSelect {...p} value={f.vpc_id} onChange={set("vpc_id")}>
                <option value="">Select…</option>
                {(vpcs.data ?? []).map((v) => (
                  <option key={v.vpc_id} value={v.vpc_id}>
                    {v.vpc_id} {v.name ? `(${v.name})` : ""} {v.cidr_block}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <fieldset className="grid gap-3 rounded-md border p-3">
            <legend className="px-1 text-xs font-medium">Health check</legend>
            <FormField label="Path" error={errors.path}>
              {(p) => <Input {...p} value={f.path} onChange={set("path")} />}
            </FormField>
            <div className="grid grid-cols-3 gap-3">
              <FormField label="Interval (s)" error={errors.interval}>
                {(p) => <Input {...p} type="number" value={f.interval} onChange={set("interval")} />}
              </FormField>
              <FormField label="Healthy threshold" error={errors.healthy}>
                {(p) => <Input {...p} type="number" value={f.healthy} onChange={set("healthy")} />}
              </FormField>
              <FormField label="Unhealthy threshold" error={errors.unhealthy}>
                {(p) => <Input {...p} type="number" value={f.unhealthy} onChange={set("unhealthy")} />}
              </FormField>
            </div>
          </fieldset>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create target group
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function TargetGroupDetail({ id }: { id: string }) {
  const q = useTargetGroup(id);
  const health = useTargetHealth(id);
  const update = useUpdateTargetGroup(id);
  const deregister = useDeregisterTarget(id);
  const del = useDeleteTargetGroup();
  const router = useRouter();
  const [registerOpen, setRegisterOpen] = useState(false);
  const [toDeregister, setToDeregister] = useState<Target[]>([]);
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const tg = q.data;
  const targets = health.data ?? tg.targets ?? [];

  const columns: Column<Target>[] = [
    {
      id: "id",
      header: "Instance",
      sortable: true,
      cell: (t) => (
        <Link className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400" href={`/ec2/instances/${encodeURIComponent(t.id)}`}>
          {t.id}
        </Link>
      ),
    },
    { id: "port", header: "Port", sortable: true, cell: (t) => t.port ?? tg.port },
    { id: "weight", header: "Weight", sortable: true, cell: (t) => t.weight ?? 1 },
    { id: "health", header: "Health", sortable: true, cell: (t) => <StatusBadge status={t.health} /> },
    { id: "reason", header: "Reason", cell: (t) => <span className="text-xs text-muted-foreground">{t.reason || "—"}</span> },
  ];

  return (
    <div className="grid gap-4">
      <PageHeader
        title={tg.name}
        description={<Mono copy>{tg.target_group_id}</Mono>}
        breadcrumbs={[
          { label: "Load Balancers", href: "/elb" },
          { label: "Target groups", href: "/elb/target-groups" },
          { label: tg.name },
        ]}
        actions={
          <Button variant="destructive" size="sm" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete
          </Button>
        }
      />
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "Protocol : Port", value: `${tg.protocol}:${tg.port}` },
            { label: "VPC", value: <Mono>{tg.vpc_id}</Mono> },
            { label: "ARN", value: <Mono copy>{tg.arn}</Mono> },
            {
              label: "Algorithm",
              value: (
                <NativeSelect
                  aria-label="Load balancing algorithm"
                  className="h-7 max-w-64"
                  value={tg.algorithm}
                  disabled={update.isPending}
                  onChange={(e) =>
                    update.mutate(
                      { algorithm: e.target.value as TargetGroup["algorithm"] },
                      { onSuccess: () => toast.success("Algorithm updated") },
                    )
                  }
                >
                  {ALGORITHMS.map((a) => (
                    <option key={a.value} value={a.value}>
                      {a.label}
                    </option>
                  ))}
                </NativeSelect>
              ),
            },
            {
              label: "Health check",
              value: tg.health_check
                ? `${tg.health_check.path} every ${tg.health_check.interval_s}s (healthy ${tg.health_check.healthy_threshold} / unhealthy ${tg.health_check.unhealthy_threshold})`
                : "—",
            },
          ]}
        />
      </Section>
      <Section title="Registered targets">
        <ResourceTable
          label="Registered targets"
          columns={columns}
          getRowId={(t) => t.id}
          data={targets}
          isLoading={health.isLoading && !tg.targets}
          isFetching={health.isFetching}
          onRetry={() => void health.refetch()}
          globalSearch={false}
          selectable
          bulkActions={[{ label: "Deregister", destructive: true, icon: <Trash2 />, onClick: (rows) => setToDeregister(rows) }]}
          toolbar={
            <Button size="sm" onClick={() => setRegisterOpen(true)}>
              <Plus /> Register targets
            </Button>
          }
          emptyTitle="No registered targets"
          emptyDescription="Register running instances from the same VPC. Health is refreshed every 5 seconds."
        />
      </Section>
      <RegisterTargetsDialog tg={tg} open={registerOpen} onOpenChange={setRegisterOpen} registered={targets.map((t) => t.id)} />
      <ConfirmDialog
        open={toDeregister.length > 0}
        onOpenChange={(o) => !o && setToDeregister([])}
        title={`Deregister ${toDeregister.length} target(s)?`}
        description={<span className="font-mono text-xs">{toDeregister.map((t) => t.id).join(", ")}</span>}
        confirmLabel="Deregister"
        destructive
        onConfirm={async () => {
          for (const t of toDeregister) await deregister.mutateAsync(t.id);
          toast.success(`Deregistered ${toDeregister.length} target(s)`);
        }}
      />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete target group ${tg.name}?`}
        description="Deletion fails while a listener still forwards to this target group."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          await del.mutateAsync(tg.target_group_id);
          toast.success(`Target group ${tg.name} deleted`);
          router.push("/elb/target-groups");
        }}
      />
    </div>
  );
}

function RegisterTargetsDialog({
  tg,
  open,
  onOpenChange,
  registered,
}: {
  tg: TargetGroup;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  registered: string[];
}) {
  const instances = useInstanceOptions(tg.vpc_id ? { vpc_id: tg.vpc_id } : {});
  const reg = useRegisterTargets(tg.target_group_id);
  const [selected, setSelected] = useState<string[]>([]);
  const [port, setPort] = useState(String(tg.port));
  const [weight, setWeight] = useState("1");
  const [err, setErr] = useState<string | null>(null);

  const options = (instances.data ?? [])
    .filter((i) => i.state !== "terminated" && !registered.includes(i.instance_id))
    .map((i) => ({ value: i.instance_id, label: i.name, hint: i.state }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const p = Number(port);
    const w = Number(weight);
    if (selected.length === 0) return setErr("Select at least one instance.");
    if (!Number.isInteger(p) || p < 1 || p > 65535) return setErr("Port must be 1–65535.");
    if (!Number.isInteger(w) || w < 0 || w > 999) return setErr("Weight must be a whole number from 0 to 999.");
    setErr(null);
    try {
      await reg.mutateAsync(selected.map((id) => ({ id, port: p, weight: w })));
      toast.success(`Registered ${selected.length} target(s)`);
      setSelected([]);
      onOpenChange(false);
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Register targets</DialogTitle>
          <DialogDescription>Instances in {tg.vpc_id || "the target group's VPC"}.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="grid gap-3" noValidate>
          <FormError message={err} />
          <FormField label="Instances" required>
            {(p) => (
              <CheckboxList
                id={p.id}
                label="Instances"
                options={options}
                value={selected}
                onChange={setSelected}
                emptyText={instances.isLoading ? "Loading instances…" : "No unregistered instances in this VPC."}
              />
            )}
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <FormField label="Port">{(p) => <Input {...p} type="number" value={port} onChange={(e) => setPort(e.target.value)} />}</FormField>
            <FormField label="Weight" hint="Used by weighted random.">
              {(p) => <Input {...p} type="number" value={weight} onChange={(e) => setWeight(e.target.value)} />}
            </FormField>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={reg.isPending}>
              {reg.isPending && <Loader2 className="animate-spin" />} Register
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
