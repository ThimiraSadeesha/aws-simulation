"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { Play, Plus, RotateCw, Square, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { relativeTime } from "@/lib/format";
import type { Instance } from "@/lib/types";
import { useInstances, useInstanceTypes, type InstanceAction } from "@/features/ec2/api";
import { canDo, InstanceActionDialog } from "@/features/ec2/instance-actions";

const STATES = ["pending", "running", "stopping", "stopped", "rebooting", "shutting-down", "terminated"];

export default function InstancesPage() {
  const list = useListParams({ sort: "launch_time", order: "desc" });
  const query = useInstances(list.params);
  const types = useInstanceTypes();
  const [pending, setPending] = useState<{ action: InstanceAction; ids: string[] } | null>(null);

  const columns = useMemo<Column<Instance>[]>(
    () => [
      {
        id: "name",
        header: "Name",
        sortable: true,
        cell: (i) => (
          <Link href={`/ec2/instances/${i.instance_id}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
            {i.name || i.tags?.Name || "(no name)"}
          </Link>
        ),
      },
      { id: "instance_id", header: "Instance ID", sortable: true, cell: (i) => <Mono copy>{i.instance_id}</Mono> },
      { id: "state", header: "State", sortable: true, cell: (i) => <StatusBadge status={i.state} /> },
      { id: "instance_type", header: "Type", sortable: true, cell: (i) => <span className="font-mono text-xs">{i.instance_type}</span> },
      { id: "private_ip", header: "Private IP", cell: (i) => <Mono>{i.private_ip}</Mono> },
      { id: "public_ip", header: "Public IP", cell: (i) => <Mono>{i.public_ip}</Mono> },
      { id: "vpc_id", header: "VPC", sortable: true, cell: (i) => <Mono>{i.vpc_id}</Mono> },
      { id: "launch_time", header: "Launched", sortable: true, cell: (i) => <span className="text-xs text-muted-foreground">{relativeTime(i.launch_time)}</span> },
    ],
    [],
  );

  const bulk = (action: InstanceAction, label: string, icon: React.ReactNode, destructive = false) => ({
    label,
    icon,
    destructive,
    disabled: (rows: Instance[]) => !rows.some((r) => canDo(action, r.state)),
    onClick: (rows: Instance[]) =>
      setPending({ action, ids: rows.filter((r) => canDo(action, r.state)).map((r) => r.instance_id) }),
  });

  return (
    <div>
      <PageHeader
        title="Instances"
        description="Simulated virtual machines. State transitions are driven by the CloudSim engine."
        actions={
          <Button asChild size="sm">
            <Link href="/ec2/launch">
              <Plus /> Launch instances
            </Link>
          </Button>
        }
      />
      <ResourceTable
        label="Instances"
        columns={columns}
        getRowId={(i) => i.instance_id}
        data={query.data}
        list={list}
        isLoading={query.isLoading}
        isFetching={query.isFetching}
        error={query.error}
        onRetry={() => void query.refetch()}
        selectable
        filters={[
          { key: "state", label: "State", options: STATES.map((s) => ({ value: s, label: s })) },
          {
            key: "instance_type",
            label: "Type",
            options: (types.data ?? []).map((t) => ({ value: t.name, label: t.name })),
          },
        ]}
        bulkActions={[
          bulk("start", "Start", <Play />),
          bulk("stop", "Stop", <Square />),
          bulk("reboot", "Reboot", <RotateCw />),
          bulk("terminate", "Terminate", <Trash2 />, true),
        ]}
        emptyTitle="No instances"
        emptyDescription="Launch your first simulated instance to get started."
        emptyAction={
          <Button asChild size="sm" variant="outline">
            <Link href="/ec2/launch">Launch instances</Link>
          </Button>
        }
      />
      <InstanceActionDialog action={pending?.action ?? null} ids={pending?.ids ?? []} onClose={() => setPending(null)} />
    </div>
  );
}
