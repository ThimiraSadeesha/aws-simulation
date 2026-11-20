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
import { validateCidrFormat, validatePortRange } from "@/lib/validation";
import type { NetworkAclEntry } from "@/lib/types";
import { useAddNaclEntry, useAllSubnets, useAssociateNacl, useDeleteNaclEntry, useNetworkAcl } from "@/features/vpc/api";
import { IdLink, PROTOCOL_OPTIONS, portRange, protocolLabel } from "@/features/vpc/components/common";

function EntryForm({ aclId }: { aclId: string }) {
  const add = useAddNaclEntry(aclId);
  const [f, setF] = useState({ rule_number: "100", egress: "false", protocol: "tcp", from: "80", to: "80", cidr: "0.0.0.0/0", action: "allow" });
  const [touched, setTouched] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const portless = f.protocol === "-1" || f.protocol === "icmp";
  const rn = Number(f.rule_number);
  const rnErr = Number.isInteger(rn) && rn >= 1 && rn <= 32766 ? null : "Rule number must be 1–32766.";
  const from = portless ? 0 : Number(f.from);
  const to = portless ? 65535 : Number(f.to);
  const portErr = validatePortRange(f.protocol, from, to);
  const cidrErr = validateCidrFormat(f.cidr);
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value });

  return (
    <form
      noValidate
      aria-label="Add network ACL entry"
      className="grid gap-3 rounded-md border p-3"
      onSubmit={(e) => {
        e.preventDefault();
        setTouched(true);
        if (rnErr || portErr || cidrErr) return;
        setErr(null);
        const entry: NetworkAclEntry = {
          rule_number: rn,
          egress: f.egress === "true",
          protocol: f.protocol,
          from_port: from,
          to_port: to,
          cidr: f.cidr.trim(),
          action: f.action as "allow" | "deny",
        };
        add.mutate(entry, {
          onSuccess: () => {
            toast.success(`Entry #${rn} added`);
            setTouched(false);
          },
          onError: (er) => setErr(errorMessage(er)),
        });
      }}
    >
      <h3 className="text-sm font-medium">Add entry</h3>
      <FormError message={err} />
      <div className="grid gap-3 sm:grid-cols-3 lg:grid-cols-7">
        <FormField label="Rule #" error={touched ? rnErr : null}>
          {(p) => <Input {...p} type="number" value={f.rule_number} onChange={set("rule_number")} />}
        </FormField>
        <FormField label="Direction">
          {(p) => (
            <NativeSelect {...p} value={f.egress} onChange={set("egress")}>
              <option value="false">Inbound</option>
              <option value="true">Outbound</option>
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Protocol">
          {(p) => (
            <NativeSelect {...p} value={f.protocol} onChange={set("protocol")}>
              {PROTOCOL_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="From port" error={touched && !portless ? portErr : null}>
          {(p) => <Input {...p} type="number" disabled={portless} value={portless ? "" : f.from} onChange={set("from")} />}
        </FormField>
        <FormField label="To port">
          {(p) => <Input {...p} type="number" disabled={portless} value={portless ? "" : f.to} onChange={set("to")} />}
        </FormField>
        <FormField label="CIDR" error={touched ? cidrErr : null}>
          {(p) => <Input {...p} className="font-mono" value={f.cidr} onChange={set("cidr")} />}
        </FormField>
        <FormField label="Action">
          {(p) => (
            <NativeSelect {...p} value={f.action} onChange={set("action")}>
              <option value="allow">Allow</option>
              <option value="deny">Deny</option>
            </NativeSelect>
          )}
        </FormField>
      </div>
      <div>
        <Button type="submit" size="sm" disabled={add.isPending}>
          <Plus /> Add entry
        </Button>
      </div>
    </form>
  );
}

export default function NetworkAclDetailPage() {
  const { id } = useParams<{ id: string }>();
  const q = useNetworkAcl(id);
  const subnets = useAllSubnets(q.data?.vpc_id);
  const delEntry = useDeleteNaclEntry(id);
  const associate = useAssociateNacl(id);
  const [toDelete, setToDelete] = useState<NetworkAclEntry | null>(null);
  const [subnetId, setSubnetId] = useState("");

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const acl = q.data;

  const cols: Column<NetworkAclEntry>[] = [
    { id: "rule_number", header: "Rule #", sortable: true, cell: (e) => e.rule_number },
    { id: "protocol", header: "Protocol", cell: (e) => protocolLabel(e.protocol) },
    { id: "ports", header: "Port range", cell: (e) => portRange(e.protocol, e.from_port, e.to_port) },
    { id: "cidr", header: "CIDR", cell: (e) => <span className="font-mono text-xs">{e.cidr}</span> },
    { id: "action", header: "Action", cell: (e) => <StatusBadge status={e.action} /> },
    {
      id: "actions",
      header: "",
      cell: (e) => (
        <Button size="icon-xs" variant="ghost" aria-label={`Delete ${e.egress ? "outbound" : "inbound"} entry ${e.rule_number}`} onClick={() => setToDelete(e)}>
          <Trash2 />
        </Button>
      ),
    },
  ];
  const inbound = (acl.entries ?? []).filter((e) => !e.egress);
  const outbound = (acl.entries ?? []).filter((e) => e.egress);
  const footer = <p className="mt-2 text-xs text-muted-foreground">Rule * (implicit): deny all traffic not matched above.</p>;

  return (
    <div className="grid gap-4">
      <PageHeader
        breadcrumbs={[{ label: "VPC", href: "/vpc" }, { label: "Network ACLs", href: "/vpc/network-acls" }, { label: acl.network_acl_id }]}
        title={acl.name || acl.network_acl_id}
      />
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "Network ACL ID", value: <Mono copy>{acl.network_acl_id}</Mono> },
            { label: "VPC", value: <IdLink id={acl.vpc_id} /> },
            { label: "Default", value: acl.is_default ? "Yes" : "No" },
          ]}
        />
      </Section>
      <Section title={`Inbound entries (${inbound.length})`}>
        <ResourceTable label="Inbound entries" columns={cols} getRowId={(e) => `in-${e.rule_number}`} data={inbound} hideSearch globalSearch={false} emptyTitle="No inbound entries" />
        {footer}
      </Section>
      <Section title={`Outbound entries (${outbound.length})`}>
        <ResourceTable label="Outbound entries" columns={cols} getRowId={(e) => `out-${e.rule_number}`} data={outbound} hideSearch globalSearch={false} emptyTitle="No outbound entries" />
        {footer}
      </Section>
      <Section title="Edit entries">
        <EntryForm aclId={acl.network_acl_id} />
      </Section>
      <Section title="Subnet associations">
        <div className="grid gap-3">
          <p className="text-sm">{acl.subnet_ids?.length ? <span className="font-mono text-xs">{acl.subnet_ids.join(", ")}</span> : <span className="text-muted-foreground">No associated subnets.</span>}</p>
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
                  {(subnets.data ?? [])
                    .filter((s) => !acl.subnet_ids?.includes(s.subnet_id))
                    .map((s) => (
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
        title={`Delete ${toDelete?.egress ? "outbound" : "inbound"} entry #${toDelete?.rule_number}?`}
        destructive
        confirmLabel="Delete entry"
        onConfirm={() => delEntry.mutateAsync({ rule_number: toDelete!.rule_number, egress: toDelete!.egress })}
      />
    </div>
  );
}
