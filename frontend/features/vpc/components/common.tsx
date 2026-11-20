"use client";

import Link from "next/link";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FormError, NativeSelect } from "@/components/console/form-field";
import { useAllNatGateways, useAllInternetGateways, useAllSecurityGroups, useAllSubnets, useAllVpcs } from "../api";

/** Dialog wrapping a <form>; renders the inline API error and a pending submit button. */
export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  onSubmit,
  pending,
  submitLabel = "Create",
  error,
  children,
  wide,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  description?: React.ReactNode;
  onSubmit: () => void;
  pending?: boolean;
  submitLabel?: string;
  error?: string | null;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={wide ? "sm:max-w-2xl" : "sm:max-w-lg"}>
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            onSubmit();
          }}
          className="grid gap-4"
        >
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description && <DialogDescription>{description}</DialogDescription>}
          </DialogHeader>
          <FormError message={error} />
          <div className="grid gap-3">{children}</div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
              Cancel
            </Button>
            <Button type="submit" disabled={pending}>
              {pending && <Loader2 className="animate-spin" />}
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

type SelectProps = Omit<React.ComponentProps<"select">, "onChange" | "value"> & {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
};

export function VpcSelect({ value, onChange, placeholder = "Select a VPC", ...rest }: SelectProps) {
  const q = useAllVpcs();
  return (
    <NativeSelect {...rest} value={value} onChange={(e) => onChange(e.target.value)} disabled={q.isLoading || rest.disabled}>
      <option value="">{q.isLoading ? "Loading…" : placeholder}</option>
      {(q.data ?? []).map((v) => (
        <option key={v.vpc_id} value={v.vpc_id}>
          {v.vpc_id} {v.name ? `(${v.name})` : ""} · {v.cidr_block}
          {v.is_default ? " · default" : ""}
        </option>
      ))}
    </NativeSelect>
  );
}

export function SubnetSelect({ value, onChange, vpcId, placeholder = "Select a subnet", ...rest }: SelectProps & { vpcId?: string }) {
  const q = useAllSubnets(vpcId);
  return (
    <NativeSelect {...rest} value={value} onChange={(e) => onChange(e.target.value)} disabled={q.isLoading || rest.disabled}>
      <option value="">{q.isLoading ? "Loading…" : placeholder}</option>
      {(q.data ?? []).map((s) => (
        <option key={s.subnet_id} value={s.subnet_id}>
          {s.subnet_id} {s.name ? `(${s.name})` : ""} · {s.cidr_block} · {s.is_public ? "public" : "private"}
        </option>
      ))}
    </NativeSelect>
  );
}

export function SecurityGroupMultiSelect({
  value,
  onChange,
  vpcId,
  id,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  vpcId?: string;
  id?: string;
}) {
  const q = useAllSecurityGroups(vpcId);
  const groups = q.data ?? [];
  if (q.isLoading) return <p className="text-xs text-muted-foreground">Loading security groups…</p>;
  if (groups.length === 0) return <p className="text-xs text-muted-foreground">No security groups{vpcId ? " in this VPC" : ""}.</p>;
  return (
    <div id={id} role="group" className="grid max-h-40 gap-1 overflow-y-auto rounded-md border p-2">
      {groups.map((g) => (
        <label key={g.group_id} className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={value.includes(g.group_id)}
            onChange={(e) =>
              onChange(e.target.checked ? [...value, g.group_id] : value.filter((x) => x !== g.group_id))
            }
          />
          <span className="font-mono text-xs">{g.group_id}</span>
          <span className="truncate text-muted-foreground">{g.group_name}</span>
        </label>
      ))}
    </div>
  );
}

/** Route target options: local, IGWs attached to the VPC, NAT gateways in the VPC. */
export function useRouteTargets(vpcId?: string) {
  const igws = useAllInternetGateways();
  const nats = useAllNatGateways();
  const options = [
    ...(igws.data ?? [])
      .filter((g) => !vpcId || g.vpc_id === vpcId)
      .map((g) => ({ value: g.internet_gateway_id, label: `${g.internet_gateway_id}${g.name ? ` (${g.name})` : ""} · IGW` })),
    ...(nats.data ?? [])
      .filter((n) => (!vpcId || n.vpc_id === vpcId) && n.state !== "deleted" && n.state !== "deleting")
      .map((n) => ({ value: n.nat_gateway_id, label: `${n.nat_gateway_id}${n.name ? ` (${n.name})` : ""} · NAT (${n.state})` })),
  ];
  return { options, isLoading: igws.isLoading || nats.isLoading };
}

const ROUTES: Record<string, string> = {
  "vpc-": "/vpc/vpcs/",
  "rtb-": "/vpc/route-tables/",
  "sg-": "/vpc/security-groups/",
  "acl-": "/vpc/network-acls/",
  "i-": "/ec2/instances/",
};

/** Monospace resource ID that links to its detail page when one exists. */
export function IdLink({ id }: { id: string | null | undefined }) {
  if (!id) return <span className="text-muted-foreground">—</span>;
  const prefix = Object.keys(ROUTES).find((p) => id.startsWith(p));
  if (!prefix) return <span className="font-mono text-xs">{id}</span>;
  return (
    <Link
      href={`${ROUTES[prefix]}${encodeURIComponent(id)}`}
      className="font-mono text-xs text-sky-700 hover:underline dark:text-sky-400"
      onClick={(e) => e.stopPropagation()}
    >
      {id}
    </Link>
  );
}

export const PROTOCOL_OPTIONS = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "icmp", label: "ICMP" },
  { value: "-1", label: "All traffic (-1)" },
];

export function protocolLabel(p: string) {
  return PROTOCOL_OPTIONS.find((o) => o.value === String(p))?.label ?? p;
}

export function portRange(protocol: string, from: number, to: number) {
  if (String(protocol) === "-1") return "All";
  if (String(protocol) === "icmp") return "N/A";
  return from === to ? String(from) : `${from}–${to}`;
}
