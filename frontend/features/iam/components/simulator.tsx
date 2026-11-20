"use client";

import { useState } from "react";
import { FlaskConical, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { errorMessage } from "@/lib/api/client";
import type { SimulateResponse } from "@/lib/types";
import { parsePolicyJson } from "@/lib/validation";
import { useAllIamRoles, useAllIamUsers, useSimulate } from "../api";
import { COMMON_ACTIONS } from "../types";

const CUSTOM = "__custom__";

const DECISION_TEXT: Record<string, string> = {
  allowed: "Allowed — an Allow statement matched and no explicit Deny applied.",
  explicitDeny: "Explicitly denied — a Deny statement matched (explicit Deny always wins).",
  implicitDeny: "Implicitly denied — no statement allowed this request.",
};

export function PolicySimulator() {
  const users = useAllIamUsers();
  const roles = useAllIamRoles();
  const simulate = useSimulate();
  const [principal, setPrincipal] = useState("");
  const [customArn, setCustomArn] = useState("");
  const [action, setAction] = useState("ec2:RunInstances");
  const [resource, setResource] = useState("*");
  const [context, setContext] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [apiErr, setApiErr] = useState<string | null>(null);
  const [result, setResult] = useState<SimulateResponse | null>(null);

  const principalArn = principal === CUSTOM ? customArn.trim() : principal;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    const errs: Record<string, string> = {};
    if (!principalArn) errs.principal = "Choose a principal or enter an ARN.";
    else if (!principalArn.startsWith("arn:cloudsim:")) errs.principal = "Principal must be a CloudSim ARN (arn:cloudsim:iam::…).";
    if (!/^[a-z0-9-]+:[A-Za-z0-9*?]+$/.test(action.trim())) errs.action = "Action must look like service:Operation, e.g. s3:GetObject.";
    if (!resource.trim()) errs.resource = "Resource ARN is required (use * for any).";
    let ctx: Record<string, unknown> | undefined;
    if (context.trim()) {
      const p = parsePolicyJson(context);
      if (!p.value) errs.context = p.error ?? "Context must be a JSON object.";
      else ctx = p.value;
    }
    setErrors(errs);
    if (Object.keys(errs).length) return;
    try {
      const r = await simulate.mutateAsync({ principal_arn: principalArn, action: action.trim(), resource_arn: resource.trim(), ...(ctx ? { context: ctx } : {}) });
      setResult(r);
    } catch (err) {
      setResult(null);
      setApiErr(errorMessage(err));
    }
  };

  return (
    <div className="grid gap-4">
      <PageHeader title="Policy simulator" description="Evaluate whether a principal is allowed to perform an action on a resource, using the same policy evaluation engine as real requests." />
      <Section title="Request">
        <form onSubmit={submit} noValidate className="grid gap-3">
          <FormError message={apiErr} />
          <div className="grid gap-3 md:grid-cols-2">
            <FormField label="Principal" required error={errors.principal}>
              {(p) => (
                <NativeSelect {...p} value={principal} onChange={(e) => setPrincipal(e.target.value)}>
                  <option value="">{users.isLoading || roles.isLoading ? "Loading principals…" : "Select a user or role"}</option>
                  {(users.data ?? []).length > 0 && (
                    <optgroup label="Users">
                      {users.data!.map((u) => (
                        <option key={u.arn} value={u.arn}>
                          {u.user_name}
                        </option>
                      ))}
                    </optgroup>
                  )}
                  {(roles.data ?? []).length > 0 && (
                    <optgroup label="Roles">
                      {roles.data!.map((r) => (
                        <option key={r.arn} value={r.arn}>
                          {r.role_name}
                        </option>
                      ))}
                    </optgroup>
                  )}
                  <option value={CUSTOM}>Enter an ARN…</option>
                </NativeSelect>
              )}
            </FormField>
            {principal === CUSTOM && (
              <FormField label="Principal ARN" required>
                {(p) => <Input {...p} className="font-mono" placeholder="arn:cloudsim:iam::123456789012:user/alice" value={customArn} onChange={(e) => setCustomArn(e.target.value)} />}
              </FormField>
            )}
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <FormField label="Action" required error={errors.action} hint="service:Operation — wildcards * and ? are allowed in policies, not here.">
              {(p) => <Input {...p} list="iam-actions" className="font-mono" value={action} onChange={(e) => setAction(e.target.value)} />}
            </FormField>
            <datalist id="iam-actions">
              {COMMON_ACTIONS.map((a) => (
                <option key={a} value={a} />
              ))}
            </datalist>
            <FormField label="Resource ARN" required error={errors.resource} hint="e.g. arn:cloudsim:s3:::my-bucket/* or *">
              {(p) => <Input {...p} className="font-mono" value={resource} onChange={(e) => setResource(e.target.value)} />}
            </FormField>
          </div>
          <FormField label="Context (optional JSON)" error={errors.context} hint='Condition keys, e.g. {"cloudsim:RequestTag/env": "dev"}'>
            {(p) => <Textarea {...p} rows={3} className="font-mono text-xs" value={context} onChange={(e) => setContext(e.target.value)} />}
          </FormField>
          <div>
            <Button type="submit" disabled={simulate.isPending}>
              {simulate.isPending ? <Loader2 className="animate-spin" /> : <FlaskConical />} Run simulation
            </Button>
          </div>
        </form>
      </Section>
      {result && (
        <Section title="Result">
          <div className="mb-3 flex flex-wrap items-center gap-3" role="status">
            <StatusBadge status={result.decision} className="text-sm" />
            <span className="text-sm text-muted-foreground">{DECISION_TEXT[result.decision] ?? result.decision}</span>
          </div>
          <ResourceTable
            label="Matched statements"
            data={result.matched_statements ?? []}
            getRowId={(s) => `${s.policy}-${s.sid}-${s.effect}`}
            hideSearch
            globalSearch={false}
            emptyTitle="No statements matched"
            emptyDescription="With no matching Allow, the request is implicitly denied."
            columns={[
              { id: "policy", header: "Policy", sortable: true, cell: (s) => s.policy },
              { id: "sid", header: "Sid", cell: (s) => <span className="font-mono text-xs">{s.sid || "—"}</span> },
              { id: "effect", header: "Effect", sortable: true, cell: (s) => <StatusBadge status={s.effect === "Allow" ? "allow" : "deny"} label={s.effect} /> },
            ]}
          />
        </Section>
      )}
    </div>
  );
}
