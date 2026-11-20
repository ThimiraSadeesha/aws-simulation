"use client";

import Link from "next/link";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import type { LoadBalancer } from "@/lib/types";
import { useCreateLoadBalancer, useDeleteLoadBalancer, useLoadBalancers } from "./api";
import { CheckboxList } from "./checkbox-list";
import { useSecurityGroupOptions, useSubnetOptions } from "./lookups";
import { toast } from "sonner";

export function LoadBalancersPage() {
  const list = useListParams({ sort: "name" });
  const q = useLoadBalancers(list.params);
  const del = useDeleteLoadBalancer();
  const router = useRouter();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<LoadBalancer[]>([]);

  const columns: Column<LoadBalancer>[] = [
    {
      id: "name",
      header: "Name",
      sortable: true,
      cell: (r) => (
        <Link href={`/elb/load-balancers/${encodeURIComponent(r.load_balancer_id)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400" onClick={(e) => e.stopPropagation()}>
          {r.name}
        </Link>
      ),
    },
    { id: "load_balancer_id", header: "ID", sortable: true, cell: (r) => <Mono>{r.load_balancer_id}</Mono> },
    { id: "state", header: "State", sortable: true, cell: (r) => <StatusBadge status={r.state} /> },
    { id: "scheme", header: "Scheme", sortable: true, cell: (r) => r.scheme },
    { id: "dns_name", header: "DNS name", cell: (r) => <Mono copy>{r.dns_name}</Mono> },
    { id: "vpc_id", header: "VPC", sortable: true, cell: (r) => <Mono>{r.vpc_id}</Mono> },
    { id: "listeners", header: "Listeners", cell: (r) => (r.listeners ?? []).map((l) => `${l.protocol}:${l.port}`).join(", ") || "—" },
  ];

  return (
    <div>
      <PageHeader
        title="Load balancers"
        description="Simulated application load balancers distribute requests across healthy targets in target groups."
        breadcrumbs={[{ label: "Load Balancers" }]}
      />
      <ResourceTable
        label="Load balancers"
        columns={columns}
        getRowId={(r) => r.load_balancer_id}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        filters={[
          {
            key: "scheme",
            label: "Scheme",
            options: [
              { value: "internet-facing", label: "internet-facing" },
              { value: "internal", label: "internal" },
            ],
          },
        ]}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: (rows) => setToDelete(rows) }]}
        onRowClick={(r) => router.push(`/elb/load-balancers/${encodeURIComponent(r.load_balancer_id)}`)}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create load balancer
          </Button>
        }
        emptyTitle="No load balancers"
        emptyDescription="Create a load balancer, then add a listener that forwards to a target group."
      />
      <CreateLoadBalancerDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length} load balancer(s)?`}
        description={<span className="font-mono text-xs">{toDelete.map((l) => l.name).join(", ")}</span>}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const lb of toDelete) await del.mutateAsync(lb.load_balancer_id);
          toast.success(`Deletion requested for ${toDelete.length} load balancer(s)`);
        }}
      />
    </div>
  );
}

function CreateLoadBalancerDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [name, setName] = useState("");
  const [scheme, setScheme] = useState<"internet-facing" | "internal">("internet-facing");
  const [subnets, setSubnets] = useState<string[]>([]);
  const [sgs, setSgs] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const subnetsQ = useSubnetOptions();
  const vpcOfFirst = subnetsQ.data?.find((s) => s.subnet_id === subnets[0])?.vpc_id;
  const sgQ = useSecurityGroupOptions(vpcOfFirst);
  const create = useCreateLoadBalancer();
  const router = useRouter();

  const reset = () => {
    setName("");
    setScheme("internet-facing");
    setSubnets([]);
    setSgs([]);
    setErrors({});
    create.reset();
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    if (!/^[a-zA-Z0-9-]{1,32}$/.test(name) || name.startsWith("-") || name.endsWith("-"))
      errs.name = "1–32 characters: letters, digits and hyphens; must not begin or end with a hyphen.";
    if (subnets.length < 1) errs.subnets = "Select at least one subnet.";
    const vpcs = new Set(subnets.map((id) => subnetsQ.data?.find((s) => s.subnet_id === id)?.vpc_id));
    if (vpcs.size > 1) errs.subnets = "All subnets must belong to the same VPC.";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    const lb = await create.mutateAsync({ name, scheme, subnet_ids: subnets, security_group_ids: sgs }).catch(() => null);
    if (lb) {
      toast.success(`Load balancer ${lb.name} is provisioning`);
      onOpenChange(false);
      reset();
      if (lb.load_balancer_id) router.push(`/elb/load-balancers/${encodeURIComponent(lb.load_balancer_id)}`);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Create load balancer</DialogTitle>
          <DialogDescription>The load balancer is placed in the VPC of the selected subnets.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="grid gap-3" noValidate>
          <FormError message={create.error ? errorMessage(create.error) : null} />
          <FormField label="Name" required error={errors.name}>
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} placeholder="web-alb" />}
          </FormField>
          <FormField label="Scheme">
            {(p) => (
              <NativeSelect {...p} value={scheme} onChange={(e) => setScheme(e.target.value as typeof scheme)}>
                <option value="internet-facing">internet-facing</option>
                <option value="internal">internal</option>
              </NativeSelect>
            )}
          </FormField>
          <FormField label="Subnets" required error={errors.subnets} hint="At least one required; two or more (in different subnets) recommended.">
            {(p) => (
              <CheckboxList
                id={p.id}
                label="Subnets"
                value={subnets}
                onChange={setSubnets}
                emptyText={subnetsQ.isLoading ? "Loading subnets…" : "No subnets – create one in VPC first."}
                options={(subnetsQ.data ?? []).map((s) => ({
                  value: s.subnet_id,
                  label: `${s.name || ""} ${s.cidr_block}`.trim(),
                  hint: `${s.vpc_id}${s.is_public ? " · public" : ""}`,
                }))}
              />
            )}
          </FormField>
          <FormField label="Security groups" hint={vpcOfFirst ? `Groups in ${vpcOfFirst}` : "Select a subnet to list its VPC's security groups."}>
            {(p) => (
              <CheckboxList
                id={p.id}
                label="Security groups"
                value={sgs}
                onChange={setSgs}
                emptyText={vpcOfFirst ? "No security groups" : "—"}
                options={vpcOfFirst ? (sgQ.data ?? []).map((g) => ({ value: g.group_id, label: g.group_name })) : []}
              />
            )}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create load balancer
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
