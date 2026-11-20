"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, Lock, Plus, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField } from "@/components/console/form-field";
import { JsonEditor, prettyJson } from "@/components/console/json-editor";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { IamPolicy } from "@/lib/types";
import { parsePolicyJson, validateIamName } from "@/lib/validation";
import { policyIsBuiltin, useCreatePolicy, useDeletePolicy, useIamPolicies, useIamPolicy, useUpdatePolicy } from "../api";
import { DEFAULT_POLICY } from "../types";

function TypeBadge({ p }: { p: IamPolicy }) {
  const builtin = policyIsBuiltin(p);
  return (
    <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] ${builtin ? "bg-sky-500/10 text-sky-700 dark:text-sky-400" : "bg-muted text-muted-foreground"}`}>
      {builtin && <Lock className="size-3" aria-hidden />}
      {builtin ? "Built-in" : "Customer managed"}
    </span>
  );
}

export function PoliciesPage() {
  const list = useListParams({ sort: "policy_name" });
  const q = useIamPolicies(list.params);
  const del = useDeletePolicy();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<IamPolicy[]>([]);
  return (
    <div>
      <PageHeader title="Policies" description="Managed policies are JSON documents (Version, Statement[] with Effect, Action, Resource, Condition) attached to users, groups and roles." />
      <ResourceTable
        label="IAM policies"
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(p) => p.arn || p.policy_name}
        selectable
        bulkActions={[
          {
            label: "Delete",
            icon: <Trash2 />,
            destructive: true,
            disabled: (rows) => rows.some(policyIsBuiltin),
            onClick: setToDelete,
          },
        ]}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create policy
          </Button>
        }
        emptyTitle="No policies"
        columns={[
          {
            id: "policy_name",
            header: "Policy name",
            sortable: true,
            cell: (p) => (
              <Link href={`/iam/policies/${encodeURIComponent(p.policy_name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
                {p.policy_name}
              </Link>
            ),
          },
          { id: "type", header: "Type", cell: (p) => <TypeBadge p={p} /> },
          { id: "description", header: "Description", cell: (p) => <span className="line-clamp-1">{p.description || "—"}</span> },
          { id: "attachment_count", header: "Attachments", cell: (p) => p.attachment_count ?? "—" },
          { id: "updated_at", header: "Updated", sortable: true, cell: (p) => formatDate(p.updated_at ?? p.created_at) },
        ]}
      />
      {createOpen && <CreatePolicyDialog onClose={() => setCreateOpen(false)} />}
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length === 1 ? `policy ${toDelete[0]?.policy_name}` : `${toDelete.length} policies`}?`}
        description="A policy that is still attached may be rejected by the backend (DependencyViolation). Detach it first."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const p of toDelete) await del.mutateAsync(p.policy_name);
        }}
      />
    </div>
  );
}

function CreatePolicyDialog({ onClose }: { onClose: () => void }) {
  const router = useRouter();
  const create = useCreatePolicy();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [doc, setDoc] = useState(prettyJson(DEFAULT_POLICY));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [apiErr, setApiErr] = useState<string | null>(null);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    const errs: Record<string, string> = {};
    const n = validateIamName(name, "Policy name");
    if (n) errs.name = n;
    const parsed = parsePolicyJson(doc);
    if (!parsed.value) errs.doc = parsed.error ?? "Invalid policy";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    try {
      await create.mutateAsync({ policy_name: name, policy_document: parsed.value!, ...(description ? { description } : {}) });
      onClose();
      router.push(`/iam/policies/${encodeURIComponent(name)}`);
    } catch (e2) {
      setApiErr(errorMessage(e2));
    }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-3xl">
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create policy</DialogTitle>
            <DialogDescription>Use “Validate policy” to check the document with the backend grammar before saving.</DialogDescription>
          </DialogHeader>
          <FormError message={apiErr} />
          <div className="grid gap-3 sm:grid-cols-2">
            <FormField label="Policy name" required error={errors.name}>
              {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
            </FormField>
            <FormField label="Description">
              {(p) => <Textarea {...p} rows={1} value={description} onChange={(e) => setDescription(e.target.value)} />}
            </FormField>
          </div>
          <JsonEditor label="Policy document" value={doc} onChange={setDoc} validatePolicy height={320} />
          {errors.doc && (
            <p role="alert" className="text-xs text-destructive">
              {errors.doc}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create policy
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function PolicyDetail({ name }: { name: string }) {
  const q = useIamPolicy(name);
  const crumbs = [{ label: "IAM", href: "/iam" }, { label: "Policies", href: "/iam/policies" }, { label: name }];
  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data)
    return (
      <div>
        <PageHeader title={name} breadcrumbs={crumbs} />
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      </div>
    );
  // key forces the editor state to reset when a new version is loaded
  return <PolicyEditorView key={`${q.data.arn}-${q.data.default_version_id ?? ""}`} policy={q.data} crumbs={crumbs} />;
}

function PolicyEditorView({ policy, crumbs }: { policy: IamPolicy; crumbs: { label: string; href?: string }[] }) {
  const router = useRouter();
  const builtin = policyIsBuiltin(policy);
  const update = useUpdatePolicy();
  const del = useDeletePolicy();
  const [doc, setDoc] = useState(prettyJson(policy.policy_document ?? {}));
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const dirty = doc !== prettyJson(policy.policy_document ?? {});

  const save = async () => {
    setError(null);
    const parsed = parsePolicyJson(doc);
    if (!parsed.value) return setError(parsed.error);
    try {
      const r = await update.mutateAsync({ name: policy.policy_name, document: parsed.value });
      toast.success(`Saved ${policy.policy_name}${r?.default_version_id ? ` as version ${r.default_version_id}` : ""}`);
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  return (
    <div className="grid gap-4">
      <PageHeader
        title={policy.policy_name}
        description={policy.description}
        breadcrumbs={crumbs}
        actions={
          !builtin && (
            <>
              <Button size="sm" onClick={() => void save()} disabled={!dirty || update.isPending}>
                {update.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save new version
              </Button>
              <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
                <Trash2 /> Delete
              </Button>
            </>
          )
        }
      />
      <Section title="Summary">
        <DetailGrid
          items={[
            { label: "ARN", value: <Mono copy>{policy.arn}</Mono> },
            { label: "Type", value: <TypeBadge p={policy} /> },
            { label: "Default version", value: policy.default_version_id ?? "—" },
            { label: "Attachments", value: policy.attachment_count ?? "—" },
            { label: "Created", value: formatDate(policy.created_at) },
            { label: "Updated", value: formatDate(policy.updated_at) },
          ]}
        />
      </Section>
      <Section title="Policy document">
        {builtin && <p className="mb-2 text-xs text-muted-foreground">Built-in policies are read-only.</p>}
        <FormError message={error} />
        <JsonEditor label="Policy document" value={doc} onChange={setDoc} readOnly={builtin} validatePolicy={!builtin} height={420} />
      </Section>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete policy ${policy.policy_name}?`}
        description="Detach the policy from all principals first. This cannot be undone."
        confirmLabel="Delete policy"
        destructive
        onConfirm={async () => {
          await del.mutateAsync(policy.policy_name);
          router.push("/iam/policies");
        }}
      />
    </div>
  );
}
