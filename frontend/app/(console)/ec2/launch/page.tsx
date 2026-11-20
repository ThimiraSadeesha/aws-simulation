"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { Loader2, Rocket } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Checkbox } from "@/components/ui/checkbox";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/console/page-header";
import { Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { KeyValueEditor, kvToRecord, type KV } from "@/components/console/key-value-editor";
import { errorMessage } from "@/lib/api/client";
import { hasErrors, MAX_LAUNCH_COUNT, validateLaunch, type FieldErrors } from "@/lib/validation";
import type { RunInstancesRequest, Subnet } from "@/lib/types";
import { useImages, useInstanceTypes, useKeyPairs, useRunInstances } from "@/features/ec2/api";
import { useSecurityGroupOptions, useSubnetOptions, useVpcOptions } from "@/features/ec2/network-api";

export default function LaunchPage() {
  const router = useRouter();
  const images = useImages();
  const types = useInstanceTypes();
  const keyPairs = useKeyPairs();
  const vpcs = useVpcOptions();
  const subnets = useSubnetOptions();
  const sgs = useSecurityGroupOptions();
  const run = useRunInstances();

  const [name, setName] = useState("");
  const [tags, setTags] = useState<KV[]>([]);
  const [imageId, setImageId] = useState("");
  const [instanceType, setInstanceType] = useState("");
  const [subnetId, setSubnetId] = useState("");
  const [publicIp, setPublicIp] = useState(true);
  const [sgIds, setSgIds] = useState<string[]>([]);
  const [keyName, setKeyName] = useState("");
  const [userData, setUserData] = useState("");
  const [count, setCount] = useState("1");
  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);

  const subnet = subnets.data?.find((s) => s.subnet_id === subnetId);
  const vpcName = (id: string) => {
    const v = vpcs.data?.find((x) => x.vpc_id === id);
    return v ? `${v.name || v.vpc_id}${v.is_default ? " (default)" : ""} · ${v.cidr_block}` : id;
  };
  const subnetsByVpc = useMemo(() => {
    const m = new Map<string, Subnet[]>();
    for (const s of subnets.data ?? []) {
      const arr = m.get(s.vpc_id) ?? [];
      arr.push(s);
      m.set(s.vpc_id, arr);
    }
    return m;
  }, [subnets.data]);
  const defaultVpcId = vpcs.data?.find((v) => v.is_default)?.vpc_id;
  const sgVpc = subnet?.vpc_id ?? defaultVpcId;
  const visibleSgs = (sgs.data ?? []).filter((g) => !sgVpc || g.vpc_id === sgVpc);
  const sgVpcIds = Object.fromEntries((sgs.data ?? []).map((g) => [g.group_id, g.vpc_id]));
  const selectedType = types.data?.find((t) => t.name === instanceType);
  const selectedImage = images.data?.find((i) => i.image_id === imageId);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);
    const n = Number(count);
    const v = validateLaunch(
      {
        name,
        image_id: imageId,
        instance_type: instanceType,
        count: n,
        subnet_id: subnetId,
        security_group_ids: sgIds,
        key_name: keyName,
        user_data: userData,
        tags,
      },
      { subnetVpcId: subnet?.vpc_id, sgVpcIds },
    );
    setErrors(v);
    if (hasErrors(v)) {
      setFormError("Fix the highlighted fields and try again.");
      return;
    }
    const body: RunInstancesRequest = { image_id: imageId, instance_type: instanceType, count: n, associate_public_ip: publicIp };
    if (name.trim()) body.name = name.trim();
    if (keyName) body.key_name = keyName;
    if (subnetId) body.subnet_id = subnetId;
    if (sgIds.length) body.security_group_ids = sgIds;
    if (userData) body.user_data = userData;
    const t = kvToRecord(tags);
    if (Object.keys(t).length) body.tags = t;
    try {
      const res = await run.mutateAsync(body);
      const ids = (res?.instances ?? []).map((i) => i.instance_id);
      toast.success(`Launched ${ids.length} instance${ids.length === 1 ? "" : "s"}`, { description: ids.join(", ") });
      router.push(ids.length === 1 ? `/ec2/instances/${ids[0]}` : "/ec2");
    } catch (err) {
      setFormError(errorMessage(err));
    }
  };

  return (
    <form onSubmit={submit} noValidate className="grid max-w-4xl gap-4">
      <PageHeader title="Launch instances" description="Configure and launch simulated instances. Nothing is created in a real cloud." />
      <FormError message={formError} />

      <Section title="Name and tags">
        <div className="grid gap-4">
          <FormField label="Name" error={errors.name} hint="Stored as the Name tag.">
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} placeholder="web-1" />}
          </FormField>
          <div className="grid gap-1.5">
            <span className="text-xs font-medium">Additional tags</span>
            <KeyValueEditor value={tags} onChange={setTags} />
            {errors.tags && (
              <p role="alert" className="text-xs text-destructive">
                {errors.tags}
              </p>
            )}
          </div>
        </div>
      </Section>

      <Section title="Application and OS image">
        <FormField label="Image (AMI)" required error={errors.image_id} hint={selectedImage?.description}>
          {(p) => (
            <NativeSelect {...p} value={imageId} onChange={(e) => setImageId(e.target.value)} disabled={images.isLoading}>
              <option value="">{images.isLoading ? "Loading images…" : images.isError ? "Failed to load images" : "Select an image"}</option>
              {(images.data ?? []).map((i) => (
                <option key={i.image_id} value={i.image_id}>
                  {i.name} ({i.os}, {i.architecture}) — {i.image_id}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
      </Section>

      <Section title="Instance type">
        <FormField
          label="Instance type"
          required
          error={errors.instance_type}
          hint={
            selectedType
              ? `${selectedType.vcpu} vCPU · ${selectedType.memory_mib} MiB · simulated $${selectedType.hourly_price_sim}/hour`
              : undefined
          }
        >
          {(p) => (
            <NativeSelect {...p} value={instanceType} onChange={(e) => setInstanceType(e.target.value)} disabled={types.isLoading}>
              <option value="">{types.isLoading ? "Loading types…" : "Select an instance type"}</option>
              {(types.data ?? []).map((t) => (
                <option key={t.name} value={t.name}>
                  {t.name} — {t.vcpu} vCPU, {t.memory_mib} MiB
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
      </Section>

      <Section title="Key pair (login)">
        <FormField
          label="Key pair"
          hint={
            <>
              Optional. <Link href="/ec2/key-pairs" className="underline">Create a key pair</Link>
            </>
          }
        >
          {(p) => (
            <NativeSelect {...p} value={keyName} onChange={(e) => setKeyName(e.target.value)}>
              <option value="">Proceed without a key pair</option>
              {(keyPairs.data?.items ?? []).map((k) => (
                <option key={k.name} value={k.name}>
                  {k.name}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
      </Section>

      <Section title="Network settings">
        <div className="grid gap-4">
          <FormField label="Subnet" hint="Without a subnet the account's default VPC/subnet is used (created on first use).">
            {(p) => (
              <NativeSelect
                {...p}
                value={subnetId}
                onChange={(e) => {
                  setSubnetId(e.target.value);
                  setSgIds([]);
                  const s = subnets.data?.find((x) => x.subnet_id === e.target.value);
                  if (s) setPublicIp(s.map_public_ip_on_launch || s.is_public);
                }}
              >
                <option value="">Default VPC / default subnet</option>
                {[...subnetsByVpc.entries()].map(([vpcId, list]) => (
                  <optgroup key={vpcId} label={vpcName(vpcId)}>
                    {list.map((s) => (
                      <option key={s.subnet_id} value={s.subnet_id}>
                        {s.name || s.subnet_id} · {s.cidr_block} · {s.is_public ? "public" : "private"}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <div className="flex items-center gap-2">
            <Switch id="public-ip" checked={publicIp} onCheckedChange={setPublicIp} />
            <Label htmlFor="public-ip" className="text-sm">
              Auto-assign public IP
            </Label>
          </div>
          <fieldset className="grid gap-1.5">
            <legend className="mb-1 text-xs font-medium">Security groups</legend>
            {sgs.isLoading ? (
              <p className="text-xs text-muted-foreground">Loading security groups…</p>
            ) : visibleSgs.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No security groups in this VPC yet{subnetId ? "" : " — the default security group will be used"}.
              </p>
            ) : (
              <ul className="grid gap-1 sm:grid-cols-2">
                {visibleSgs.map((g) => {
                  const id = `sg-${g.group_id}`;
                  return (
                    <li key={g.group_id} className="flex items-center gap-2 rounded-md border px-2 py-1.5">
                      <Checkbox
                        id={id}
                        checked={sgIds.includes(g.group_id)}
                        onCheckedChange={(c) => setSgIds((s) => (c ? [...s, g.group_id] : s.filter((x) => x !== g.group_id)))}
                      />
                      <Label htmlFor={id} className="min-w-0 flex-1 text-sm font-normal">
                        <span className="truncate">{g.group_name}</span>{" "}
                        <span className="font-mono text-xs text-muted-foreground">{g.group_id}</span>
                      </Label>
                    </li>
                  );
                })}
              </ul>
            )}
            {errors.security_group_ids && (
              <p role="alert" className="text-xs text-destructive">
                {errors.security_group_ids}
              </p>
            )}
            <p className="text-xs text-muted-foreground">If none is selected, the VPC&apos;s default security group is used.</p>
          </fieldset>
        </div>
      </Section>

      <Section title="Advanced details">
        <div className="grid gap-4">
          <FormField
            label="User data"
            error={errors.user_data}
            hint="Up to 16 KiB. CloudSim parses and echoes the script into the console output; it is not executed."
          >
            {(p) => (
              <Textarea
                {...p}
                value={userData}
                onChange={(e) => setUserData(e.target.value)}
                rows={6}
                className="font-mono text-xs"
                placeholder={"#!/bin/bash\necho hello"}
              />
            )}
          </FormField>
          <FormField label="Number of instances" required error={errors.count} hint={`1–${MAX_LAUNCH_COUNT}`}>
            {(p) => (
              <Input {...p} type="number" min={1} max={MAX_LAUNCH_COUNT} value={count} onChange={(e) => setCount(e.target.value)} className="w-32" />
            )}
          </FormField>
        </div>
      </Section>

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={() => router.push("/ec2")}>
          Cancel
        </Button>
        <Button type="submit" disabled={run.isPending}>
          {run.isPending ? <Loader2 className="animate-spin" /> : <Rocket />} Launch instance
        </Button>
      </div>
    </form>
  );
}
