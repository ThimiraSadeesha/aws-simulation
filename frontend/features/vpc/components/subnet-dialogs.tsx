"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { FormField } from "@/components/console/form-field";
import { errorMessage } from "@/lib/api/client";
import { validateSubnetCidr } from "@/lib/validation";
import type { Subnet } from "@/lib/types";
import { useAllSubnets, useAllVpcs, useCreateSubnet, usePatchSubnet } from "../api";
import { FormDialog, VpcSelect } from "./common";

export function CreateSubnetDialog({
  open,
  onOpenChange,
  defaultVpcId = "",
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  defaultVpcId?: string;
}) {
  const [vpcId, setVpcId] = useState(defaultVpcId);
  const [name, setName] = useState("");
  const [cidr, setCidr] = useState("");
  const [touched, setTouched] = useState(false);
  const [apiError, setApiError] = useState<string | null>(null);
  const vpcs = useAllVpcs();
  const siblings = useAllSubnets(vpcId || undefined);
  const create = useCreateSubnet();

  const vpc = vpcs.data?.find((v) => v.vpc_id === vpcId);
  const cidrError = validateSubnetCidr(cidr, vpc?.cidr_block, vpcId ? (siblings.data ?? []) : []);
  const vpcError = vpcId ? null : "Choose a VPC.";

  const reset = () => {
    setName("");
    setCidr("");
    setTouched(false);
    setApiError(null);
  };

  const submit = () => {
    setTouched(true);
    if (vpcError || cidrError) return;
    setApiError(null);
    create.mutate(
      { vpc_id: vpcId, cidr_block: cidr.trim(), name: name.trim() || undefined },
      {
        onSuccess: (s) => {
          toast.success(`Subnet ${s.subnet_id} created`);
          reset();
          onOpenChange(false);
        },
        onError: (e) => setApiError(errorMessage(e)),
      },
    );
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
      title="Create subnet"
      description="The subnet CIDR must be inside the VPC CIDR and must not overlap other subnets. New subnets use the VPC's main route table."
      onSubmit={submit}
      pending={create.isPending}
      submitLabel="Create subnet"
      error={apiError}
    >
      <FormField label="VPC" required error={touched ? vpcError : null}>
        {(p) => <VpcSelect {...p} name="vpc" value={vpcId} onChange={setVpcId} />}
      </FormField>
      <FormField label="Name tag">
        {(p) => <Input {...p} name="subnet-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="public-a" />}
      </FormField>
      <FormField
        label="IPv4 CIDR block"
        required
        hint={vpc ? `Within ${vpc.cidr_block}, prefix /16–/28.` : "Select a VPC first."}
        error={touched || cidr ? cidrError : null}
      >
        {(p) => (
          <Input
            {...p}
            name="subnet-cidr"
            className="font-mono"
            value={cidr}
            onChange={(e) => setCidr(e.target.value)}
            onBlur={() => setTouched(true)}
            placeholder={vpc ? vpc.cidr_block.replace(/\/\d+$/, "/24") : "10.0.1.0/24"}
          />
        )}
      </FormField>
      {vpcId && (siblings.data?.length ?? 0) > 0 && (
        <p className="text-xs text-muted-foreground">
          Existing subnets: <span className="font-mono">{siblings.data!.map((s) => s.cidr_block).join(", ")}</span>
        </p>
      )}
    </FormDialog>
  );
}

export function EditSubnetDialog({ subnet, onClose }: { subnet: Subnet | null; onClose: () => void }) {
  return subnet ? <EditSubnetInner key={subnet.subnet_id} subnet={subnet} onClose={onClose} /> : null;
}

function EditSubnetInner({ subnet, onClose }: { subnet: Subnet; onClose: () => void }) {
  const [name, setName] = useState(subnet.name);
  const [mapPublic, setMapPublic] = useState(subnet.map_public_ip_on_launch);
  const [apiError, setApiError] = useState<string | null>(null);
  const patch = usePatchSubnet();
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Edit ${subnet.subnet_id}`}
      onSubmit={() =>
        patch.mutate(
          { id: subnet.subnet_id, name, map_public_ip_on_launch: mapPublic },
          {
            onSuccess: () => {
              toast.success("Subnet updated");
              onClose();
            },
            onError: (e) => setApiError(errorMessage(e)),
          },
        )
      }
      pending={patch.isPending}
      submitLabel="Save"
      error={apiError}
    >
      <FormField label="Name tag">{(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} />}</FormField>
      <div className="flex items-center gap-3">
        <Switch id="map-public" checked={mapPublic} onCheckedChange={setMapPublic} />
        <Label htmlFor="map-public">Auto-assign public IPv4 address on launch</Label>
      </div>
    </FormDialog>
  );
}
