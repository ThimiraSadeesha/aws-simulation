"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Input } from "@/components/ui/input";
import { FormField } from "@/components/console/form-field";
import { KeyValueEditor, kvToRecord, type KV } from "@/components/console/key-value-editor";
import { errorMessage } from "@/lib/api/client";
import { validateTags, validateVpcCidr } from "@/lib/validation";
import { useCreateVpc } from "../api";
import { FormDialog } from "./common";

export function CreateVpcDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [name, setName] = useState("");
  const [cidr, setCidr] = useState("10.0.0.0/16");
  const [tags, setTags] = useState<KV[]>([]);
  const [touched, setTouched] = useState(false);
  const [apiError, setApiError] = useState<string | null>(null);
  const create = useCreateVpc();
  const router = useRouter();

  const check = validateVpcCidr(cidr);
  const tagErr = validateTags(tags);

  const reset = () => {
    setName("");
    setCidr("10.0.0.0/16");
    setTags([]);
    setTouched(false);
    setApiError(null);
  };

  const submit = () => {
    setTouched(true);
    if (check.error || tagErr) return;
    setApiError(null);
    const t = kvToRecord(tags);
    create.mutate(
      { cidr_block: cidr.trim(), name: name.trim() || undefined, tags: Object.keys(t).length ? t : undefined },
      {
        onSuccess: (vpc) => {
          toast.success(`VPC ${vpc.vpc_id} created`);
          onOpenChange(false);
          reset();
          router.push(`/vpc/vpcs/${vpc.vpc_id}`);
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
      title="Create VPC"
      description="A main route table (local route), a default network ACL (allow all) and a default security group are created with the VPC."
      onSubmit={submit}
      pending={create.isPending}
      submitLabel="Create VPC"
      error={apiError}
    >
      <FormField label="Name tag">
        {(p) => <Input {...p} name="vpc-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="my-vpc" />}
      </FormField>
      <FormField
        label="IPv4 CIDR block"
        required
        hint="Prefix /16 to /28. RFC 1918 private ranges recommended."
        error={touched ? check.error : null}
        warning={check.warning}
      >
        {(p) => (
          <Input {...p} name="cidr" className="font-mono" value={cidr} onChange={(e) => setCidr(e.target.value)} onBlur={() => setTouched(true)} />
        )}
      </FormField>
      <div className="grid gap-1.5">
        <span className="text-xs font-medium">Tags</span>
        <KeyValueEditor value={tags} onChange={setTags} />
        {tagErr && (
          <p role="alert" className="text-xs text-destructive">
            {tagErr}
          </p>
        )}
      </div>
    </FormDialog>
  );
}
