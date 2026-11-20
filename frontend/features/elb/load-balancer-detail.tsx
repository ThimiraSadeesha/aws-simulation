"use client";

import Link from "next/link";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Loader2, Play, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { StatusBadge } from "@/components/console/status-badge";
import { errorMessage } from "@/lib/api/client";
import type { LbSimulationResult, Listener, LoadBalancer } from "@/lib/types";
import { useAllTargetGroups, useCreateListener, useDeleteListener, useDeleteLoadBalancer, useLoadBalancer, useSimulateTraffic } from "./api";

export function LoadBalancerDetail({ id }: { id: string }) {
  const q = useLoadBalancer(id);
  const del = useDeleteLoadBalancer();
  const router = useRouter();
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const lb = q.data;

  return (
    <div className="grid gap-4">
      <PageHeader
        title={lb.name}
        description={<Mono copy>{lb.load_balancer_id}</Mono>}
        breadcrumbs={[{ label: "Load Balancers", href: "/elb" }, { label: lb.name }]}
        actions={
          <Button variant="destructive" size="sm" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete
          </Button>
        }
      />
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "State", value: <StatusBadge status={lb.state} /> },
            { label: "DNS name", value: <Mono copy>{lb.dns_name}</Mono> },
            { label: "Scheme", value: lb.scheme },
            { label: "VPC", value: <Mono>{lb.vpc_id}</Mono> },
            { label: "Subnets", value: <span className="font-mono text-xs">{(lb.subnet_ids ?? []).join(", ") || "—"}</span> },
            { label: "Security groups", value: <span className="font-mono text-xs">{(lb.security_group_ids ?? []).join(", ") || "—"}</span> },
            { label: "ARN", value: <Mono copy>{lb.arn}</Mono> },
          ]}
        />
      </Section>
      <ListenersSection lb={lb} />
      <SimulateSection lb={lb} />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete load balancer ${lb.name}?`}
        description="Listeners are removed with it. Target groups and their instances are not affected."
        confirmLabel="Delete"
        destructive
        typeToConfirm={lb.name}
        onConfirm={async () => {
          await del.mutateAsync(lb.load_balancer_id);
          toast.success(`Deletion of ${lb.name} requested`);
          router.push("/elb");
        }}
      />
    </div>
  );
}

function ListenersSection({ lb }: { lb: LoadBalancer }) {
  const tgs = useAllTargetGroups();
  const create = useCreateListener(lb.load_balancer_id);
  const del = useDeleteListener(lb.load_balancer_id);
  const [open, setOpen] = useState(false);
  const [port, setPort] = useState("80");
  const [tg, setTg] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<Listener | null>(null);
  const tgName = (tid: string) => tgs.data?.find((t) => t.target_group_id === tid)?.name;

  const columns: Column<Listener>[] = [
    { id: "protocol", header: "Protocol", sortable: true, cell: (l) => l.protocol },
    { id: "port", header: "Port", sortable: true, cell: (l) => l.port },
    {
      id: "default_target_group_id",
      header: "Default action",
      cell: (l) => (
        <span>
          Forward to{" "}
          <Link className="text-sky-700 hover:underline dark:text-sky-400" href={`/elb/target-groups/${encodeURIComponent(l.default_target_group_id)}`}>
            {tgName(l.default_target_group_id) ?? l.default_target_group_id}
          </Link>
        </span>
      ),
    },
    { id: "listener_id", header: "Listener ID", cell: (l) => <Mono>{l.listener_id}</Mono> },
    {
      id: "actions",
      header: "",
      cell: (l) => (
        <Button size="icon-sm" variant="ghost" aria-label={`Delete listener ${l.protocol}:${l.port}`} onClick={() => setToDelete(l)}>
          <Trash2 />
        </Button>
      ),
    },
  ];

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const p = Number(port);
    if (!Number.isInteger(p) || p < 1 || p > 65535) return setErr("Port must be between 1 and 65535.");
    if (!tg) return setErr("Choose a default target group.");
    if ((lb.listeners ?? []).some((l) => l.port === p)) return setErr(`A listener on port ${p} already exists.`);
    setErr(null);
    const r = await create.mutateAsync({ protocol: "HTTP", port: p, default_target_group_id: tg }).catch((e2: unknown) => {
      setErr(errorMessage(e2));
      return null;
    });
    if (r !== null) {
      toast.success(`Listener HTTP:${p} added`);
      setOpen(false);
    }
  };

  return (
    <Section title="Listeners">
      <ResourceTable
        label="Listeners"
        columns={columns}
        getRowId={(l) => l.listener_id}
        data={lb.listeners ?? []}
        globalSearch={false}
        hideSearch
        toolbar={
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus /> Add listener
          </Button>
        }
        emptyTitle="No listeners"
        emptyDescription="Add a listener so the load balancer accepts traffic and forwards it to a target group."
      />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add listener</DialogTitle>
          </DialogHeader>
          <form onSubmit={submit} className="grid gap-3" noValidate>
            <FormError message={err} />
            <FormField label="Protocol" hint="HTTP is the only protocol simulated in this release.">
              {(p) => (
                <NativeSelect {...p} value="HTTP" disabled>
                  <option value="HTTP">HTTP</option>
                </NativeSelect>
              )}
            </FormField>
            <FormField label="Port" required>
              {(p) => <Input {...p} type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} />}
            </FormField>
            <FormField label="Default target group" required>
              {(p) => (
                <NativeSelect {...p} value={tg} onChange={(e) => setTg(e.target.value)}>
                  <option value="">Select…</option>
                  {(tgs.data ?? [])
                    .filter((t) => !lb.vpc_id || t.vpc_id === lb.vpc_id)
                    .map((t) => (
                      <option key={t.target_group_id} value={t.target_group_id}>
                        {t.name} ({t.protocol}:{t.port})
                      </option>
                    ))}
                </NativeSelect>
              )}
            </FormField>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={create.isPending}>
                {create.isPending && <Loader2 className="animate-spin" />} Add listener
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete listener ${toDelete?.protocol}:${toDelete?.port}?`}
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          if (toDelete) await del.mutateAsync(toDelete.listener_id);
          toast.success("Listener deleted");
        }}
      />
    </Section>
  );
}

function SimulateSection({ lb }: { lb: LoadBalancer }) {
  const sim = useSimulateTraffic(lb.load_balancer_id);
  const listeners = lb.listeners ?? [];
  const [requests, setRequests] = useState("1000");
  const [port, setPort] = useState("");
  const [duration, setDuration] = useState("60");
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<LbSimulationResult | null>(null);
  const effectivePort = port || (listeners[0] ? String(listeners[0].port) : "");

  const run = async (e: React.FormEvent) => {
    e.preventDefault();
    const n = Number(requests);
    const d = Number(duration);
    if (!Number.isInteger(n) || n < 1 || n > 1_000_000) return setErr("Requests must be a whole number from 1 to 1,000,000.");
    if (!Number.isInteger(d) || d < 1 || d > 3600) return setErr("Duration must be 1–3600 seconds.");
    setErr(null);
    try {
      const r = await sim.mutateAsync({
        requests: n,
        duration_s: d,
        ...(effectivePort ? { listener_port: Number(effectivePort) } : {}),
      });
      setResult(r);
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  };

  const chartData = (result?.per_target ?? []).map((t) => ({ id: t.id, requests: t.requests }));

  return (
    <Section title="Simulate traffic">
      <p className="mb-3 text-sm text-muted-foreground">
        Sends simulated requests through a listener. They are distributed over the <em>healthy</em> targets of its target group using the group&apos;s
        algorithm, and the load raises those instances&apos; CPU for the chosen duration.
      </p>
      <form onSubmit={run} className="grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto] sm:items-end" noValidate>
        <FormField label="Requests">
          {(p) => <Input {...p} type="number" min={1} value={requests} onChange={(e) => setRequests(e.target.value)} />}
        </FormField>
        <FormField label="Listener port">
          {(p) => (
            <NativeSelect {...p} value={effectivePort} onChange={(e) => setPort(e.target.value)} disabled={listeners.length === 0}>
              {listeners.length === 0 && <option value="">No listeners</option>}
              {listeners.map((l) => (
                <option key={l.listener_id} value={l.port}>
                  {l.protocol}:{l.port}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Duration (seconds)">
          {(p) => <Input {...p} type="number" min={1} value={duration} onChange={(e) => setDuration(e.target.value)} />}
        </FormField>
        <Button type="submit" disabled={sim.isPending || listeners.length === 0}>
          {sim.isPending ? <Loader2 className="animate-spin" /> : <Play />} Simulate traffic
        </Button>
      </form>
      {listeners.length === 0 && <p className="mt-2 text-xs text-muted-foreground">Add a listener before simulating traffic.</p>}
      <div className="mt-3">
        <FormError message={err} />
      </div>
      {result && (
        <div className="mt-4 grid gap-4" data-testid="simulation-result">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <Stat label="Total" value={result.total.toLocaleString()} />
            <Stat label="Served" value={result.served.toLocaleString()} />
            <Stat label="Dropped" value={result.dropped.toLocaleString()} tone={result.dropped > 0 ? "danger" : undefined} />
            <Stat label="Avg latency" value={`${result.avg_latency_ms.toFixed(1)} ms`} />
          </div>
          {result.per_target.length === 0 ? (
            <p className="text-sm text-muted-foreground">No healthy targets – every request was dropped.</p>
          ) : (
            <>
              <div role="img" aria-label="Requests per target bar chart" className="h-56">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={chartData} margin={{ top: 5, right: 8, bottom: 0, left: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="id" tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" />
                    <YAxis tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" width={48} />
                    <Tooltip contentStyle={{ background: "var(--popover)", border: "1px solid var(--border)", borderRadius: 6, fontSize: 12 }} />
                    <Bar dataKey="requests" fill="var(--chart-1)" radius={[3, 3, 0, 0]} isAnimationActive={false} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
              <ResourceTable
                label="Distribution per target"
                hideSearch
                globalSearch={false}
                getRowId={(r) => r.id}
                data={result.per_target}
                columns={[
                  { id: "id", header: "Target", sortable: true, cell: (r) => <Mono>{r.id}</Mono> },
                  { id: "requests", header: "Requests", sortable: true, cell: (r) => r.requests.toLocaleString() },
                  { id: "percent", header: "Share", sortable: true, cell: (r) => `${r.percent.toFixed(1)}%` },
                  { id: "avg_latency_ms", header: "Avg latency", sortable: true, cell: (r) => `${r.avg_latency_ms.toFixed(1)} ms` },
                  { id: "p99_latency_ms", header: "p99 latency", sortable: true, cell: (r) => `${r.p99_latency_ms.toFixed(1)} ms` },
                ]}
              />
            </>
          )}
        </div>
      )}
    </Section>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: "danger" }) {
  return (
    <div className="rounded-md border p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`text-lg font-semibold tabular-nums ${tone === "danger" ? "text-destructive" : ""}`}>{value}</div>
    </div>
  );
}
