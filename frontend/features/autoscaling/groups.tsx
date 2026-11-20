"use client";

import Link from "next/link";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
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
import { formatDate } from "@/lib/format";
import type { AutoScalingGroup } from "@/lib/types";
import { CheckboxList } from "@/features/elb/checkbox-list";
import { useSubnetOptions } from "@/features/elb/lookups";
import { useAllTargetGroups } from "@/features/elb/api";
import { useAllLaunchTemplates, useCreateGroup, useDeleteGroup, useGroups } from "./api";

/** min ≤ desired ≤ max, non-negative integers. Returns field → message. */
export function validateCapacity(min: string, max: string, desired: string): Record<string, string> {
  const e: Record<string, string> = {};
  const [mn, mx, d] = [Number(min), Number(max), Number(desired)];
  const ok = (v: number) => Number.isInteger(v) && v >= 0 && v <= 100;
  if (!ok(mn)) e.min_size = "Whole number 0–100.";
  if (!ok(mx)) e.max_size = "Whole number 0–100.";
  if (!ok(d)) e.desired_capacity = "Whole number 0–100.";
  if (!e.min_size && !e.max_size && mn > mx) e.max_size = "Max must be ≥ min.";
  if (!Object.keys(e).length && (d < mn || d > mx)) e.desired_capacity = `Desired must be between min (${mn}) and max (${mx}).`;
  return e;
}

export function inService(g: AutoScalingGroup) {
  return (g.instances ?? []).filter((i) => i.state === "running").length;
}

export function GroupsPage() {
  const list = useListParams({ sort: "name" });
  const q = useGroups(list.params);
  const del = useDeleteGroup();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [toDelete, setToDelete] = useState<AutoScalingGroup[]>([]);
  const [force, setForce] = useState(false);

  const columns: Column<AutoScalingGroup>[] = [
    {
      id: "name",
      header: "Name",
      sortable: true,
      cell: (r) => (
        <Link href={`/autoscaling/groups/${encodeURIComponent(r.name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {r.name}
        </Link>
      ),
    },
    { id: "launch_template", header: "Launch template", sortable: true, cell: (r) => r.launch_template },
    { id: "instances", header: "Instances", cell: (r) => `${inService(r)} running / ${(r.instances ?? []).length}` },
    { id: "desired_capacity", header: "Desired", sortable: true, cell: (r) => r.desired_capacity },
    { id: "min_size", header: "Min", sortable: true, cell: (r) => r.min_size },
    { id: "max_size", header: "Max", sortable: true, cell: (r) => r.max_size },
    { id: "status", header: "Status", cell: (r) => (r.status ? <StatusBadge status={r.status} /> : "—") },
    { id: "created_at", header: "Created", sortable: true, cell: (r) => formatDate(r.created_at) },
  ];

  return (
    <div>
      <PageHeader
        title="Auto Scaling groups"
        description="Groups keep a desired number of instances running and scale with policies evaluated by the simulator."
        breadcrumbs={[{ label: "Auto Scaling" }]}
      />
      <ResourceTable
        label="Auto Scaling groups"
        columns={columns}
        getRowId={(r) => r.name}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: (rows) => setToDelete(rows) }]}
        onRowClick={(r) => router.push(`/autoscaling/groups/${encodeURIComponent(r.name)}`)}
        toolbar={
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus /> Create Auto Scaling group
          </Button>
        }
        emptyTitle="No Auto Scaling groups"
        emptyDescription="Create a launch template first, then a group that uses it."
      />
      <CreateGroupDialog open={open} onOpenChange={setOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => {
          if (!o) {
            setToDelete([]);
            setForce(false);
          }
        }}
        title={`Delete ${toDelete.length} Auto Scaling group(s)?`}
        description={<span className="font-mono text-xs">{toDelete.map((g) => g.name).join(", ")}</span>}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const grp of toDelete) await del.mutateAsync({ name: grp.name, force });
          toast.success(`Deleted ${toDelete.length} group(s)`);
          setForce(false);
        }}
      >
        <div className="flex items-center gap-2">
          <Checkbox id="asg-force" checked={force} onCheckedChange={(c) => setForce(c === true)} />
          <Label htmlFor="asg-force" className="text-sm font-normal">
            Force delete – terminate the group&apos;s instances
          </Label>
        </div>
      </ConfirmDialog>
    </div>
  );
}

function CreateGroupDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const templates = useAllLaunchTemplates();
  const subnets = useSubnetOptions();
  const tgs = useAllTargetGroups();
  const create = useCreateGroup();
  const router = useRouter();
  const initial = { name: "", launch_template: "", min_size: "1", max_size: "3", desired_capacity: "1", cooldown_s: "60", health_check_type: "EC2" };
  const [f, setF] = useState(initial);
  const [subnetIds, setSubnetIds] = useState<string[]>([]);
  const [tgIds, setTgIds] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF((s) => ({ ...s, [k]: e.target.value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validateCapacity(f.min_size, f.max_size, f.desired_capacity);
    if (!/^[a-zA-Z0-9_.-]{1,255}$/.test(f.name)) errs.name = "1–255 characters: letters, digits, '_', '.', '-'.";
    if (!f.launch_template) errs.launch_template = "Choose a launch template.";
    if (subnetIds.length === 0) errs.subnet_ids = "Select at least one subnet.";
    const cd = Number(f.cooldown_s);
    if (!Number.isInteger(cd) || cd < 0 || cd > 3600) errs.cooldown_s = "0–3600 seconds.";
    if (f.health_check_type === "ELB" && tgIds.length === 0) errs.target_group_ids = "ELB health checks need at least one target group.";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    const grp = await create
      .mutateAsync({
        name: f.name,
        launch_template: f.launch_template,
        min_size: Number(f.min_size),
        max_size: Number(f.max_size),
        desired_capacity: Number(f.desired_capacity),
        subnet_ids: subnetIds,
        target_group_ids: tgIds,
        cooldown_s: cd,
        health_check_type: f.health_check_type as "EC2" | "ELB",
      })
      .catch(() => null);
    if (grp) {
      toast.success(`Auto Scaling group ${grp.name ?? f.name} created`);
      onOpenChange(false);
      setF(initial);
      setSubnetIds([]);
      setTgIds([]);
      router.push(`/autoscaling/groups/${encodeURIComponent(grp.name ?? f.name)}`);
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
          <DialogTitle>Create Auto Scaling group</DialogTitle>
          <DialogDescription>The group launches instances from the template into the selected subnets.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="grid max-h-[70vh] gap-3 overflow-y-auto pr-1" noValidate>
          <FormError message={create.error ? errorMessage(create.error) : null} />
          <FormField label="Name" required error={errors.name}>
            {(p) => <Input {...p} value={f.name} onChange={set("name")} placeholder="web-asg" />}
          </FormField>
          <FormField label="Launch template" required error={errors.launch_template}>
            {(p) => (
              <NativeSelect {...p} value={f.launch_template} onChange={set("launch_template")}>
                <option value="">Select…</option>
                {(templates.data ?? []).map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.name} ({t.instance_type})
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <div className="grid grid-cols-3 gap-3">
            <FormField label="Min" required error={errors.min_size}>
              {(p) => <Input {...p} type="number" min={0} value={f.min_size} onChange={set("min_size")} />}
            </FormField>
            <FormField label="Desired" required error={errors.desired_capacity}>
              {(p) => <Input {...p} type="number" min={0} value={f.desired_capacity} onChange={set("desired_capacity")} />}
            </FormField>
            <FormField label="Max" required error={errors.max_size}>
              {(p) => <Input {...p} type="number" min={0} value={f.max_size} onChange={set("max_size")} />}
            </FormField>
          </div>
          <FormField label="Subnets" required error={errors.subnet_ids}>
            {(p) => (
              <CheckboxList
                id={p.id}
                label="Subnets"
                value={subnetIds}
                onChange={setSubnetIds}
                emptyText={subnets.isLoading ? "Loading…" : "No subnets"}
                options={(subnets.data ?? []).map((s) => ({ value: s.subnet_id, label: `${s.name || ""} ${s.cidr_block}`.trim(), hint: s.vpc_id }))}
              />
            )}
          </FormField>
          <FormField label="Target groups" error={errors.target_group_ids} hint="New instances are registered with these target groups.">
            {(p) => (
              <CheckboxList
                id={p.id}
                label="Target groups"
                value={tgIds}
                onChange={setTgIds}
                emptyText="No target groups"
                options={(tgs.data ?? []).map((t) => ({ value: t.target_group_id, label: t.name, hint: t.vpc_id }))}
              />
            )}
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <FormField label="Cooldown (seconds)" error={errors.cooldown_s}>
              {(p) => <Input {...p} type="number" value={f.cooldown_s} onChange={set("cooldown_s")} />}
            </FormField>
            <FormField label="Health check type">
              {(p) => (
                <NativeSelect {...p} value={f.health_check_type} onChange={set("health_check_type")}>
                  <option value="EC2">EC2</option>
                  <option value="ELB">ELB</option>
                </NativeSelect>
              )}
            </FormField>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create group
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
