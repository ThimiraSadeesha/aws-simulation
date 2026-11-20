"use client";

import { useMemo } from "react";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import type { InstanceType } from "@/lib/types";
import { useInstanceTypes } from "@/features/ec2/api";

export default function InstanceTypesPage() {
  const q = useInstanceTypes();
  const families = useMemo(() => [...new Set((q.data ?? []).map((t) => t.family))].sort(), [q.data]);
  return (
    <div>
      <PageHeader title="Instance types" description="Simulated capacity and illustrative (not real) hourly prices." />
      <ResourceTable<InstanceType>
        label="Instance types"
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(t) => t.name}
        filters={[{ key: "family", label: "Family", options: families.map((f) => ({ value: f, label: f })) }]}
        columns={[
          { id: "name", header: "Type", sortable: true, cell: (t) => <span className="font-mono text-xs font-medium">{t.name}</span> },
          { id: "family", header: "Family", sortable: true, cell: (t) => t.family },
          { id: "vcpu", header: "vCPU", sortable: true, cell: (t) => t.vcpu },
          { id: "memory_mib", header: "Memory (MiB)", sortable: true, cell: (t) => t.memory_mib.toLocaleString() },
          { id: "hourly_price_sim", header: "Simulated $/hour", sortable: true, cell: (t) => `$${t.hourly_price_sim}` },
        ]}
        emptyTitle="No instance types"
      />
    </div>
  );
}
