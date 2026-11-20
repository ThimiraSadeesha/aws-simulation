"use client";

import { useMemo } from "react";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { Mono } from "@/components/console/copy-button";
import type { MachineImage } from "@/lib/types";
import { useImages } from "@/features/ec2/api";

export default function ImagesPage() {
  const q = useImages();
  const oses = useMemo(() => [...new Set((q.data ?? []).map((t) => t.os))].sort(), [q.data]);
  return (
    <div>
      <PageHeader title="Images (AMIs)" description="Machine images available to simulated instances." />
      <ResourceTable<MachineImage>
        label="Images"
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(i) => i.image_id}
        filters={[{ key: "os", label: "OS", options: oses.map((o) => ({ value: o, label: o })) }]}
        columns={[
          { id: "name", header: "Name", sortable: true, cell: (i) => <span className="font-medium">{i.name}</span> },
          { id: "image_id", header: "Image ID", sortable: true, cell: (i) => <Mono copy>{i.image_id}</Mono> },
          { id: "os", header: "OS", sortable: true, cell: (i) => i.os },
          { id: "architecture", header: "Architecture", sortable: true, cell: (i) => i.architecture },
          { id: "description", header: "Description", cell: (i) => <span className="text-xs text-muted-foreground">{i.description}</span> },
        ]}
        emptyTitle="No images"
      />
    </div>
  );
}
