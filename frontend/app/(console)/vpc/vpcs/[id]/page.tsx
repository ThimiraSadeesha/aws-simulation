"use client";

import { useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { PageHeader } from "@/components/console/page-header";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { StatusBadge } from "@/components/console/status-badge";
import { Mono } from "@/components/console/copy-button";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { ErrorState, LoadingState, QueryBoundary } from "@/components/console/states";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { formatDate, tagsToString } from "@/lib/format";
import type { Subnet } from "@/lib/types";
import { useDeleteVpc, usePatchVpc, useTopology, useVpc } from "@/features/vpc/api";
import { TopologyDiagram } from "@/features/vpc/components/topology-diagram";
import { IdLink } from "@/features/vpc/components/common";

export default function VpcDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const q = useVpc(id);
  const [tab, setTab] = useState("details");
  const topo = useTopology(tab === "details" || tab === "topology" || tab === "subnets" ? id : "");
  const patch = usePatchVpc(id);
  const del = useDeleteVpc();
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const vpc = q.data;

  const toggle = (field: "enable_dns_support" | "enable_dns_hostnames", value: boolean) =>
    patch.mutate({ [field]: value }, { onSuccess: () => toast.success("VPC attributes updated") });

  const subnetCols: Column<Subnet>[] = [
    { id: "name", header: "Name", sortable: true, cell: (s) => s.name || "—" },
    { id: "subnet_id", header: "Subnet ID", sortable: true, cell: (s) => <Mono>{s.subnet_id}</Mono> },
    { id: "cidr_block", header: "CIDR", sortable: true, cell: (s) => <span className="font-mono text-xs">{s.cidr_block}</span> },
    { id: "is_public", header: "Type", sortable: true, cell: (s) => <StatusBadge status={s.is_public ? "available" : "stopped"} label={s.is_public ? "public" : "private"} /> },
    { id: "route_table_id", header: "Route table", cell: (s) => <IdLink id={s.route_table_id} /> },
    { id: "available_ip_count", header: "Available IPs", sortable: true, cell: (s) => s.available_ip_count },
  ];

  return (
    <div>
      <PageHeader
        breadcrumbs={[{ label: "VPC", href: "/vpc" }, { label: "VPCs", href: "/vpc" }, { label: vpc.vpc_id }]}
        title={
          <span className="flex items-center gap-2">
            {vpc.name || vpc.vpc_id} <StatusBadge status={vpc.state} />
          </span>
        }
        description={<span className="font-mono">{vpc.vpc_id} · {vpc.cidr_block}</span>}
        actions={
          <Button variant="destructive" onClick={() => setConfirmDelete(true)} disabled={vpc.is_default}>
            <Trash2 /> Delete VPC
          </Button>
        }
      />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="details">Details</TabsTrigger>
          <TabsTrigger value="topology">Topology</TabsTrigger>
          <TabsTrigger value="subnets">Subnets</TabsTrigger>
        </TabsList>
        <TabsContent value="details" className="grid gap-4">
          <Section title="Details">
            <DetailGrid
              items={[
                { label: "VPC ID", value: <Mono copy>{vpc.vpc_id}</Mono> },
                { label: "State", value: <StatusBadge status={vpc.state} /> },
                { label: "IPv4 CIDR", value: <span className="font-mono">{vpc.cidr_block}</span> },
                { label: "Default VPC", value: vpc.is_default ? "Yes" : "No" },
                { label: "Created", value: formatDate(vpc.created_at) },
                { label: "Tags", value: tagsToString(vpc.tags) || "—" },
                { label: "Subnets", value: topo.data ? topo.data.subnets.length : "…" },
                { label: "Instances", value: topo.data ? topo.data.instances.length : "…" },
                {
                  label: "Main route table",
                  value: <IdLink id={topo.data?.route_tables.find((r) => r.is_main)?.route_table_id} />,
                },
              ]}
            />
          </Section>
          <Section title="DNS settings">
            <div className="grid gap-3">
              <div className="flex items-center gap-3">
                <Switch
                  id="dns-support"
                  checked={vpc.enable_dns_support}
                  disabled={patch.isPending}
                  onCheckedChange={(v) => toggle("enable_dns_support", v)}
                />
                <Label htmlFor="dns-support">DNS resolution (enable_dns_support)</Label>
              </div>
              <div className="flex items-center gap-3">
                <Switch
                  id="dns-hostnames"
                  checked={vpc.enable_dns_hostnames}
                  disabled={patch.isPending}
                  onCheckedChange={(v) => toggle("enable_dns_hostnames", v)}
                />
                <Label htmlFor="dns-hostnames">DNS hostnames (enable_dns_hostnames)</Label>
              </div>
              <p className="text-xs text-muted-foreground">
                With DNS hostnames enabled, instance Name tags resolve as <span className="font-mono">&lt;name&gt;.{vpc.vpc_id}.cloudsim.internal</span>.
              </p>
            </div>
          </Section>
        </TabsContent>
        <TabsContent value="topology">
          <Section title="Topology">
            <QueryBoundary query={topo}>{(t) => <TopologyDiagram topology={t} />}</QueryBoundary>
          </Section>
        </TabsContent>
        <TabsContent value="subnets">
          <ResourceTable
            label={`Subnets in ${vpc.vpc_id}`}
            columns={subnetCols}
            getRowId={(s) => s.subnet_id}
            data={topo.data?.subnets}
            isLoading={topo.isLoading}
            error={topo.error}
            onRetry={() => void topo.refetch()}
            globalSearch={false}
            emptyTitle="No subnets in this VPC"
            emptyDescription="Create subnets from the Subnets page."
          />
        </TabsContent>
      </Tabs>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete ${vpc.vpc_id}?`}
        description="The VPC must have no subnets, attached internet gateways or instances, otherwise the request fails with DependencyViolation."
        destructive
        confirmLabel="Delete"
        typeToConfirm={vpc.vpc_id}
        onConfirm={async () => {
          await del.mutateAsync(vpc.vpc_id);
          toast.success(`${vpc.vpc_id} deleted`);
          router.push("/vpc");
        }}
      />
    </div>
  );
}
