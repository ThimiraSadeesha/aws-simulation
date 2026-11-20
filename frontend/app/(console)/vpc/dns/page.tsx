"use client";

import { useState } from "react";
import { Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/console/page-header";
import { Section } from "@/components/console/detail-grid";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { FormError, FormField } from "@/components/console/form-field";
import { errorMessage } from "@/lib/api/client";
import type { DnsAnswer } from "@/lib/types";
import { useDnsResolve } from "@/features/vpc/api";
import { VpcSelect } from "@/features/vpc/components/common";

const SOURCE_TEXT: Record<string, string> = {
  "vpc-private": "Resolved by the VPC's private DNS (ip-*.cloudsim.internal / instance Name tags)",
  route53: "Resolved from a Route 53 hosted zone",
  "public-sim": "Resolved by the simulated public DNS",
};

export default function DnsResolverPage() {
  const [name, setName] = useState("");
  const [vpcId, setVpcId] = useState("");
  const [touched, setTouched] = useState(false);
  const resolve = useDnsResolve();
  const nameErr = name.trim() ? null : "Enter a DNS name.";

  const cols: Column<DnsAnswer>[] = [
    { id: "type", header: "Type", sortable: true, cell: (a) => a.type },
    { id: "value", header: "Value", sortable: true, cell: (a) => <span className="font-mono text-xs">{a.value}</span> },
    { id: "ttl", header: "TTL", sortable: true, cell: (a) => a.ttl },
  ];

  return (
    <div className="grid gap-4">
      <PageHeader
        title="DNS resolver"
        description="Resolve names the way an instance in a VPC would: ip-*.cloudsim.internal, <name>.<vpc-id>.cloudsim.internal (DNS hostnames enabled) and Route 53 private zone records."
      />
      <Section title="Query">
        <form
          noValidate
          className="grid gap-3 sm:grid-cols-[2fr_2fr_auto] sm:items-end"
          onSubmit={(e) => {
            e.preventDefault();
            setTouched(true);
            if (nameErr) return;
            resolve.mutate({ name: name.trim(), vpc_id: vpcId || undefined });
          }}
        >
          <FormField label="DNS name" required error={touched ? nameErr : null}>
            {(p) => <Input {...p} className="font-mono" value={name} onChange={(e) => setName(e.target.value)} placeholder="ip-10-0-1-10.cloudsim.internal" />}
          </FormField>
          <FormField label="Resolve from VPC (optional)">
            {(p) => <VpcSelect {...p} value={vpcId} onChange={setVpcId} placeholder="No VPC (public resolution)" />}
          </FormField>
          <Button type="submit" disabled={resolve.isPending}>
            <Search /> Resolve
          </Button>
        </form>
      </Section>
      {resolve.isError && <FormError message={errorMessage(resolve.error)} />}
      {resolve.data && (
        <Section title={`Answers for ${resolve.data.name}`}>
          <p className="mb-3 text-sm text-muted-foreground">
            Source: <span className="font-mono">{resolve.data.source}</span>
            {SOURCE_TEXT[resolve.data.source] ? ` — ${SOURCE_TEXT[resolve.data.source]}` : ""}
          </p>
          <ResourceTable
            label="DNS answers"
            columns={cols}
            getRowId={(a) => `${a.type}-${a.value}`}
            data={resolve.data.answers ?? []}
            hideSearch
            globalSearch={false}
            emptyTitle="NXDOMAIN — no records found"
          />
        </Section>
      )}
    </div>
  );
}
