"use client";

import { useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Mono } from "@/components/console/copy-button";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import { MAX_USER_DATA_BYTES } from "@/lib/validation";
import type { LaunchTemplate, WorkloadProfile } from "@/lib/types";
import { useImages, useInstanceTypes, useKeyPairs } from "@/features/ec2/api";
import { CheckboxList } from "@/features/elb/checkbox-list";
import { useSecurityGroupOptions } from "@/features/elb/lookups";
import { useCreateLaunchTemplate, useLaunchTemplates } from "./api";

export const WORKLOAD_PROFILES: { value: WorkloadProfile; label: string }[] = [
  { value: "idle", label: "idle (~2–5% CPU)" },
  { value: "steady", label: "steady (~20–35% CPU)" },
  { value: "spiky", label: "spiky (bursts to 80–95%)" },
  { value: "stress", label: "stress (90–100%)" },
];

export function LaunchTemplatesPage() {
  const list = useListParams({ sort: "name" });
  const q = useLaunchTemplates(list.params);
  const [open, setOpen] = useState(false);

  const columns: Column<LaunchTemplate>[] = [
    { id: "name", header: "Name", sortable: true, cell: (r) => <span className="font-medium">{r.name}</span> },
    { id: "image_id", header: "Image", sortable: true, cell: (r) => <Mono>{r.image_id}</Mono> },
    { id: "instance_type", header: "Type", sortable: true, cell: (r) => r.instance_type },
    { id: "key_name", header: "Key pair", cell: (r) => r.key_name || "—" },
    { id: "security_group_ids", header: "Security groups", cell: (r) => <span className="font-mono text-xs">{(r.security_group_ids ?? []).join(", ") || "—"}</span> },
    { id: "workload_profile", header: "Workload", cell: (r) => r.workload_profile || "steady" },
    { id: "created_at", header: "Created", sortable: true, cell: (r) => formatDate(r.created_at) },
  ];

  return (
    <div>
      <PageHeader
        title="Launch templates"
        description="Instance configuration used by Auto Scaling groups when they launch instances."
        breadcrumbs={[{ label: "Auto Scaling", href: "/autoscaling" }, { label: "Launch templates" }]}
      />
      <ResourceTable
        label="Launch templates"
        columns={columns}
        getRowId={(r) => r.name}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        toolbar={
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus /> Create launch template
          </Button>
        }
        emptyTitle="No launch templates"
        emptyDescription="Create a launch template before creating an Auto Scaling group."
      />
      <CreateLaunchTemplateDialog open={open} onOpenChange={setOpen} />
    </div>
  );
}

function CreateLaunchTemplateDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const images = useImages();
  const types = useInstanceTypes();
  const keys = useKeyPairs();
  const sgs = useSecurityGroupOptions();
  const create = useCreateLaunchTemplate();
  const initial = { name: "", image_id: "", instance_type: "t3.micro", key_name: "", user_data: "", workload_profile: "steady" as WorkloadProfile };
  const [f, setF] = useState(initial);
  const [sgIds, setSgIds] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
    setF((s) => ({ ...s, [k]: e.target.value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    if (!/^[a-zA-Z0-9_.()/-]{3,128}$/.test(f.name)) errs.name = "3–128 characters: letters, digits and _ . ( ) / -";
    if (!f.image_id) errs.image_id = "Choose an image.";
    if (!f.instance_type) errs.instance_type = "Choose an instance type.";
    if (new TextEncoder().encode(f.user_data).length > MAX_USER_DATA_BYTES) errs.user_data = "User data must be 16 KiB or smaller.";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    const lt = await create
      .mutateAsync({
        name: f.name,
        image_id: f.image_id,
        instance_type: f.instance_type,
        workload_profile: f.workload_profile,
        ...(f.key_name ? { key_name: f.key_name } : {}),
        ...(f.user_data ? { user_data: f.user_data } : {}),
        ...(sgIds.length ? { security_group_ids: sgIds } : {}),
      })
      .catch(() => null);
    if (lt) {
      toast.success(`Launch template ${lt.name ?? f.name} created`);
      setF(initial);
      setSgIds([]);
      onOpenChange(false);
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
          <DialogTitle>Create launch template</DialogTitle>
          <DialogDescription>Subnets are chosen by the Auto Scaling group, not the template.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="grid max-h-[70vh] gap-3 overflow-y-auto pr-1" noValidate>
          <FormError message={create.error ? errorMessage(create.error) : null} />
          <FormField label="Name" required error={errors.name}>
            {(p) => <Input {...p} value={f.name} onChange={set("name")} placeholder="web-template" />}
          </FormField>
          <FormField label="Image (AMI)" required error={errors.image_id}>
            {(p) => (
              <NativeSelect {...p} value={f.image_id} onChange={set("image_id")}>
                <option value="">Select…</option>
                {(images.data ?? []).map((i) => (
                  <option key={i.image_id} value={i.image_id}>
                    {i.name} ({i.image_id})
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <FormField label="Instance type" required error={errors.instance_type}>
            {(p) => (
              <NativeSelect {...p} value={f.instance_type} onChange={set("instance_type")}>
                {(types.data ?? []).length === 0 && <option value={f.instance_type}>{f.instance_type}</option>}
                {(types.data ?? []).map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.name} – {t.vcpu} vCPU, {t.memory_mib} MiB
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <FormField label="Key pair">
            {(p) => (
              <NativeSelect {...p} value={f.key_name} onChange={set("key_name")}>
                <option value="">None</option>
                {(keys.data?.items ?? []).map((k) => (
                  <option key={k.name} value={k.name}>
                    {k.name}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <FormField label="Security groups">
            {(p) => (
              <CheckboxList
                id={p.id}
                label="Security groups"
                value={sgIds}
                onChange={setSgIds}
                options={(sgs.data ?? []).map((s) => ({ value: s.group_id, label: s.group_name, hint: s.vpc_id }))}
              />
            )}
          </FormField>
          <FormField label="Workload profile" hint="Drives simulated CPU usage of launched instances.">
            {(p) => (
              <NativeSelect {...p} value={f.workload_profile} onChange={set("workload_profile")}>
                {WORKLOAD_PROFILES.map((w) => (
                  <option key={w.value} value={w.value}>
                    {w.label}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <FormField label="User data" error={errors.user_data} hint="Parsed and echoed to the console log – not executed.">
            {(p) => <Textarea {...p} value={f.user_data} onChange={set("user_data")} rows={4} className="font-mono text-xs" placeholder="#!/bin/bash" />}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create launch template
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
