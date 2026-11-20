"use client";

import { useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { errorMessage } from "@/lib/api/client";
import { validateCidrFormat, validatePortRange } from "@/lib/validation";
import type { NewSecurityGroupRule } from "@/lib/types";
import { useAddSgRule, useAllSecurityGroups } from "../api";
import { PROTOCOL_OPTIONS } from "./common";

export function SgRuleForm({ groupId, vpcId }: { groupId: string; vpcId: string }) {
  const [direction, setDirection] = useState<"ingress" | "egress">("ingress");
  const [protocol, setProtocol] = useState<NewSecurityGroupRule["protocol"]>("tcp");
  const [from, setFrom] = useState("22");
  const [to, setTo] = useState("22");
  const [sourceType, setSourceType] = useState<"cidr" | "sg">("cidr");
  const [cidr, setCidr] = useState("0.0.0.0/0");
  const [sourceSg, setSourceSg] = useState("");
  const [description, setDescription] = useState("");
  const [touched, setTouched] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const add = useAddSgRule(groupId);
  const groups = useAllSecurityGroups(vpcId);

  const portless = protocol === "-1" || protocol === "icmp";
  const fromN = portless ? 0 : Number(from);
  const toN = portless ? 65535 : Number(to);
  const portErr = validatePortRange(protocol, fromN, toN);
  const srcErr = sourceType === "cidr" ? validateCidrFormat(cidr) : sourceSg ? null : "Choose a security group.";

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (portErr || srcErr) return;
    setErr(null);
    const body: NewSecurityGroupRule = {
      direction,
      protocol,
      from_port: fromN,
      to_port: toN,
      description: description.trim() || undefined,
      ...(sourceType === "cidr" ? { cidr: cidr.trim() } : { source_security_group_id: sourceSg }),
    };
    add.mutate(body, {
      onSuccess: () => {
        toast.success(`${direction === "ingress" ? "Inbound" : "Outbound"} rule added`);
        setTouched(false);
        setDescription("");
      },
      onError: (er) => setErr(errorMessage(er)),
    });
  };

  return (
    <form onSubmit={submit} noValidate aria-label="Add security group rule" className="grid gap-3 rounded-md border p-3">
      <h3 className="text-sm font-medium">Add rule</h3>
      <FormError message={err} />
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <FormField label="Direction">
          {(p) => (
            <NativeSelect {...p} value={direction} onChange={(e) => setDirection(e.target.value as "ingress" | "egress")}>
              <option value="ingress">Inbound (ingress)</option>
              <option value="egress">Outbound (egress)</option>
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Protocol">
          {(p) => (
            <NativeSelect {...p} value={protocol} onChange={(e) => setProtocol(e.target.value as NewSecurityGroupRule["protocol"])}>
              {PROTOCOL_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="From port" error={touched && !portless ? portErr : null}>
          {(p) => <Input {...p} type="number" min={0} max={65535} disabled={portless} value={portless ? "" : from} onChange={(e) => setFrom(e.target.value)} placeholder={portless ? "All" : ""} />}
        </FormField>
        <FormField label="To port">
          {(p) => <Input {...p} type="number" min={0} max={65535} disabled={portless} value={portless ? "" : to} onChange={(e) => setTo(e.target.value)} placeholder={portless ? "All" : ""} />}
        </FormField>
        <FormField label={direction === "ingress" ? "Source type" : "Destination type"}>
          {(p) => (
            <NativeSelect {...p} value={sourceType} onChange={(e) => setSourceType(e.target.value as "cidr" | "sg")}>
              <option value="cidr">CIDR block</option>
              <option value="sg">Security group</option>
            </NativeSelect>
          )}
        </FormField>
        {sourceType === "cidr" ? (
          <FormField label="CIDR" error={touched ? srcErr : null}>
            {(p) => <Input {...p} className="font-mono" value={cidr} onChange={(e) => setCidr(e.target.value)} />}
          </FormField>
        ) : (
          <FormField label="Security group" error={touched ? srcErr : null}>
            {(p) => (
              <NativeSelect {...p} value={sourceSg} onChange={(e) => setSourceSg(e.target.value)}>
                <option value="">Select a group</option>
                {(groups.data ?? []).map((g) => (
                  <option key={g.group_id} value={g.group_id}>
                    {g.group_id} ({g.group_name})
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
        )}
        <FormField label="Description" className="sm:col-span-2">
          {(p) => <Input {...p} value={description} onChange={(e) => setDescription(e.target.value)} />}
        </FormField>
      </div>
      <div>
        <Button type="submit" size="sm" disabled={add.isPending}>
          <Plus /> Add rule
        </Button>
      </div>
    </form>
  );
}
