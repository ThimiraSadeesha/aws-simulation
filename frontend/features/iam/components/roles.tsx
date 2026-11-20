"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, Plus, Trash2, UserCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField } from "@/components/console/form-field";
import { JsonEditor, prettyJson } from "@/components/console/json-editor";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { SecretOnceDialog } from "@/components/console/secret-once-dialog";
import { ErrorState, LoadingState } from "@/components/console/states";
import { useAuth } from "@/features/auth/auth-context";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { IamRole } from "@/lib/types";
import { parsePolicyJson, validateIamName } from "@/lib/validation";
import { useAssumeRole, useCreateRole, useDeleteRole, useIamRole, useIamRoles } from "../api";
import { defaultTrustPolicy, type AssumeRoleResponse } from "../types";
import { PermissionsPanel } from "./permissions-panel";

export function RolesPage() {
  const list = useListParams({ sort: "role_name" });
  const q = useIamRoles(list.params);
  const del = useDeleteRole();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<IamRole[]>([]);
  return (
    <div>
      <PageHeader title="Roles" description="Roles grant temporary credentials (sts:AssumeRole) to principals trusted by the role's trust policy." />
      <ResourceTable
        label="IAM roles"
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(r) => r.role_name}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create role
          </Button>
        }
        emptyTitle="No IAM roles"
        columns={[
          {
            id: "role_name",
            header: "Role name",
            sortable: true,
            cell: (r) => (
              <Link href={`/iam/roles/${encodeURIComponent(r.role_name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
                {r.role_name}
              </Link>
            ),
          },
          { id: "description", header: "Description", cell: (r) => r.description || <span className="text-muted-foreground">—</span> },
          { id: "max_session_duration", header: "Max session", cell: (r) => (r.max_session_duration ? `${r.max_session_duration} s` : "—") },
          { id: "arn", header: "ARN", cell: (r) => <Mono copy>{r.arn}</Mono> },
          { id: "created_at", header: "Created", sortable: true, cell: (r) => formatDate(r.created_at) },
        ]}
      />
      {createOpen && <CreateRoleDialog onClose={() => setCreateOpen(false)} />}
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length === 1 ? `role ${toDelete[0]?.role_name}` : `${toDelete.length} roles`}?`}
        description="Existing temporary credentials for the role stop working. This cannot be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const r of toDelete) await del.mutateAsync(r.role_name);
        }}
      />
    </div>
  );
}

function CreateRoleDialog({ onClose }: { onClose: () => void }) {
  const { user } = useAuth();
  const router = useRouter();
  const create = useCreateRole();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [duration, setDuration] = useState("3600");
  const [trust, setTrust] = useState(() => prettyJson(defaultTrustPolicy(user?.account_id)));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [apiErr, setApiErr] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    const errs: Record<string, string> = {};
    const n = validateIamName(name, "Role name");
    if (n) errs.role_name = n;
    const d = Number(duration);
    if (!Number.isInteger(d) || d < 900 || d > 43200) errs.duration = "Max session duration must be 900–43200 seconds.";
    const parsed = parsePolicyJson(trust);
    if (!parsed.value) errs.trust = parsed.error ?? "Invalid trust policy";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    try {
      await create.mutateAsync({
        role_name: name,
        assume_role_policy_document: parsed.value!,
        max_session_duration: d,
        ...(description ? { description } : {}),
      });
      onClose();
      router.push(`/iam/roles/${encodeURIComponent(name)}`);
    } catch (e2) {
      setApiErr(errorMessage(e2));
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-3xl">
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create role</DialogTitle>
            <DialogDescription>The trust policy defines which principals may assume the role.</DialogDescription>
          </DialogHeader>
          <FormError message={apiErr} />
          <div className="grid gap-3 sm:grid-cols-2">
            <FormField label="Role name" required error={errors.role_name}>
              {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
            </FormField>
            <FormField label="Max session duration (seconds)" error={errors.duration}>
              {(p) => <Input {...p} type="number" min={900} max={43200} value={duration} onChange={(e) => setDuration(e.target.value)} />}
            </FormField>
          </div>
          <FormField label="Description">
            {(p) => <Textarea {...p} rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />}
          </FormField>
          <div className="grid gap-1.5">
            <span className="text-xs font-medium">Trust policy</span>
            <JsonEditor label="Trust policy document" value={trust} onChange={setTrust} validatePolicy height={260} />
            {errors.trust && (
              <p role="alert" className="text-xs text-destructive">
                {errors.trust}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create role
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function RoleDetail({ name }: { name: string }) {
  const router = useRouter();
  const q = useIamRole(name);
  const del = useDeleteRole();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const crumbs = [{ label: "IAM", href: "/iam" }, { label: "Roles", href: "/iam/roles" }, { label: name }];
  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data)
    return (
      <div>
        <PageHeader title={name} breadcrumbs={crumbs} />
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      </div>
    );
  const r = q.data;
  return (
    <div className="grid gap-4">
      <PageHeader
        title={r.role_name}
        description={r.description}
        breadcrumbs={crumbs}
        actions={
          <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete role
          </Button>
        }
      />
      <Section title="Summary">
        <DetailGrid
          items={[
            { label: "ARN", value: <Mono copy>{r.arn}</Mono> },
            { label: "Max session duration", value: r.max_session_duration ? `${r.max_session_duration} s` : "—" },
            { label: "Created", value: formatDate(r.created_at) },
          ]}
        />
      </Section>
      <Section title="Trust policy">
        <JsonEditor label="Trust policy (read-only)" value={prettyJson(r.assume_role_policy_document)} onChange={() => {}} readOnly height={220} />
      </Section>
      <PermissionsPanel kind="roles" name={r.role_name} attached={r.attached_policies} inline={r.inline_policies} />
      <AssumeRoleForm role={r} />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete role ${r.role_name}?`}
        confirmLabel="Delete role"
        destructive
        onConfirm={async () => {
          await del.mutateAsync(r.role_name);
          router.push("/iam/roles");
        }}
      />
    </div>
  );
}

function AssumeRoleForm({ role }: { role: IamRole }) {
  const assume = useAssumeRole();
  const [session, setSession] = useState("console-session");
  const [duration, setDuration] = useState("3600");
  const [error, setError] = useState<string | null>(null);
  const [creds, setCreds] = useState<AssumeRoleResponse | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!/^[\w+=,.@-]{2,64}$/.test(session)) return setError("Session name must be 2–64 characters: letters, digits and + = , . @ _ -");
    try {
      const r = await assume.mutateAsync({ role_arn: role.arn, role_session_name: session, duration_seconds: Number(duration) || undefined });
      setCreds(r);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <Section title="Assume role (POST /sts/assume-role)">
      <form onSubmit={submit} noValidate className="grid gap-3">
        <p className="text-xs text-muted-foreground">
          The console session acts as the account root user. The request succeeds only if the caller is allowed by its own policies and by this role&apos;s trust policy.
        </p>
        <FormError message={error} />
        <div className="grid gap-3 sm:grid-cols-3">
          <FormField label="Role session name" required>
            {(p) => <Input {...p} value={session} onChange={(e) => setSession(e.target.value)} />}
          </FormField>
          <FormField label="Duration (seconds)">
            {(p) => <Input {...p} type="number" min={900} value={duration} onChange={(e) => setDuration(e.target.value)} />}
          </FormField>
          <div className="flex items-end">
            <Button type="submit" disabled={assume.isPending}>
              {assume.isPending ? <Loader2 className="animate-spin" /> : <UserCheck />} Assume role
            </Button>
          </div>
        </div>
      </form>
      {creds && (
        <SecretOnceDialog
          open
          onClose={() => setCreds(null)}
          title={`Temporary credentials for ${creds.assumed_role_user?.arn ?? role.role_name}`}
          fields={[
            { label: "Access key ID", value: creds.credentials.access_key_id },
            { label: "Secret access key", value: creds.credentials.secret_access_key },
            { label: "Session token", value: creds.credentials.session_token, multiline: true },
            { label: "Expiration", value: creds.credentials.expiration },
          ]}
          download={{
            filename: `${role.role_name}-credentials.json`,
            content: JSON.stringify(creds, null, 2),
          }}
        />
      )}
    </Section>
  );
}
