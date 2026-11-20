"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import { Link2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/console/page-header";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { Mono } from "@/components/console/copy-button";
import { ErrorState, LoadingState } from "@/components/console/states";
import { errorMessage } from "@/lib/api/client";
import { validateCidrFormat } from "@/lib/validation";
import type { Route } from "@/lib/types";
import { useAddRoute, useAllSubnets, useAssociateRouteTable, useDeleteRoute, useRouteTable } from "@/features/vpc/api";
import { IdLink, useRouteTargets } from "@/features/vpc/components/common";

export default function RouteTableDetailPage() {
  const { id } = useParams<{ id: string }>();
  const q = useRouteTable(id);
  const rt = q.data;
  const targets = useRouteTargets(rt?.vpc_id);
  const subnets = useAllSubnets(rt?.vpc_id);
  const addRoute = useAddRoute(id);
  const delRoute = useDeleteRoute(id);
  const associate = useAssociateRouteTable(id);

  const [dest, setDest] = useState("0.0.0.0/0");
  const [target, setTarget] = useState("");
  const [customTarget, setCustomTarget] = useState("");
  const [routeErr, setRouteErr] = useState<string | null>(null);
  const [touched, setTouched] = useState(false);
  const [toDelete, setToDelete] = useState<Route | null>(null);
  const [subnetId, setSubnetId] = useState("");

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !rt) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;

  const destError = validateCidrFormat(dest);
  const targetId = target === "__custom" ? customTarget.trim() : target;

  const submitRoute = (e: React.FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (destError || !targetId) return;
    setRouteErr(null);
    addRoute.mutate(
      { destination_cidr: dest.trim(), target_id: targetId },
      {
        onSuccess: () => {
          toast.success(`Route ${dest} → ${targetId} added`);
          setTouched(false);
        },
        onError: (err) => setRouteErr(errorMessage(err)),
      },
    );
  };

  const routeCols: Column<Route>[] = [
    { id: "destination_cidr", header: "Destination", sortable: true, cell: (r) => <span className="font-mono text-xs">{r.destination_cidr}</span> },
    { id: "target_id", header: "Target", sortable: true, cell: (r) => <span className="font-mono text-xs">{r.target_id}</span> },
    { id: "state", header: "State", sortable: true, cell: (r) => <StatusBadge status={r.state} /> },
    {
      id: "actions",
      header: "",
      cell: (r) =>
        r.target_id === "local" ? null : (
          <Button size="icon-xs" variant="ghost" aria-label={`Delete route ${r.destination_cidr}`} onClick={() => setToDelete(r)}>
            <Trash2 />
          </Button>
        ),
    },
  ];

  const unassociated = (subnets.data ?? []).filter((s) => !rt.subnet_ids.includes(s.subnet_id));

  return (
    <div className="grid gap-4">
      <PageHeader
        breadcrumbs={[{ label: "VPC", href: "/vpc" }, { label: "Route tables", href: "/vpc/route-tables" }, { label: rt.route_table_id }]}
        title={rt.name || rt.route_table_id}
        description={<span className="font-mono">{rt.route_table_id}</span>}
      />
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "Route table ID", value: <Mono copy>{rt.route_table_id}</Mono> },
            { label: "VPC", value: <IdLink id={rt.vpc_id} /> },
            { label: "Main", value: rt.is_main ? "Yes (used by subnets without an explicit association)" : "No" },
          ]}
        />
      </Section>
      <Section title={`Routes (${rt.routes.length})`}>
        <div className="grid gap-4">
          <ResourceTable
            label="Routes"
            columns={routeCols}
            getRowId={(r) => r.destination_cidr}
            data={rt.routes}
            globalSearch={false}
            hideSearch
            emptyTitle="No routes"
          />
          <form onSubmit={submitRoute} noValidate className="grid gap-3 rounded-md border p-3" aria-label="Add route">
            <h3 className="text-sm font-medium">Add route</h3>
            <FormError message={routeErr} />
            <div className="grid gap-3 sm:grid-cols-3">
              <FormField label="Destination CIDR" required error={touched ? destError : null}>
                {(p) => <Input {...p} className="font-mono" value={dest} onChange={(e) => setDest(e.target.value)} />}
              </FormField>
              <FormField label="Target" required error={touched && !targetId ? "Choose a target." : null}>
                {(p) => (
                  <NativeSelect {...p} value={target} onChange={(e) => setTarget(e.target.value)}>
                    <option value="">{targets.isLoading ? "Loading…" : "Select target"}</option>
                    {targets.options.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                    <option value="__custom">Other (type an ID)…</option>
                  </NativeSelect>
                )}
              </FormField>
              {target === "__custom" && (
                <FormField label="Target ID" hint="e.g. igw-…, nat-…">
                  {(p) => <Input {...p} className="font-mono" value={customTarget} onChange={(e) => setCustomTarget(e.target.value)} />}
                </FormField>
              )}
            </div>
            <div>
              <Button type="submit" size="sm" disabled={addRoute.isPending}>
                <Plus /> Add route
              </Button>
            </div>
          </form>
        </div>
      </Section>
      <Section title="Subnet associations">
        <div className="grid gap-3">
          {rt.subnet_ids.length === 0 ? (
            <p className="text-sm text-muted-foreground">No explicit subnet associations.</p>
          ) : (
            <ul className="grid gap-1 text-sm">
              {rt.subnet_ids.map((s) => {
                const sn = subnets.data?.find((x) => x.subnet_id === s);
                return (
                  <li key={s} className="flex items-center gap-2">
                    <span className="font-mono text-xs">{s}</span>
                    {sn && (
                      <span className="text-muted-foreground">
                        {sn.name} {sn.cidr_block}
                      </span>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (!subnetId) return;
              associate.mutate(subnetId, {
                onSuccess: () => {
                  toast.success(`Associated ${subnetId}`);
                  setSubnetId("");
                },
              });
            }}
          >
            <FormField label="Associate subnet" className="min-w-64">
              {(p) => (
                <NativeSelect {...p} value={subnetId} onChange={(e) => setSubnetId(e.target.value)}>
                  <option value="">Select a subnet</option>
                  {unassociated.map((s) => (
                    <option key={s.subnet_id} value={s.subnet_id}>
                      {s.subnet_id} {s.name ? `(${s.name})` : ""} · {s.cidr_block}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </FormField>
            <Button type="submit" size="sm" variant="outline" disabled={!subnetId || associate.isPending}>
              <Link2 /> Associate
            </Button>
          </form>
        </div>
      </Section>
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Delete route?"
        description={
          <span className="font-mono">
            {toDelete?.destination_cidr} → {toDelete?.target_id}
          </span>
        }
        destructive
        confirmLabel="Delete route"
        onConfirm={() => delRoute.mutateAsync(toDelete!.destination_cidr)}
      />
    </div>
  );
}
