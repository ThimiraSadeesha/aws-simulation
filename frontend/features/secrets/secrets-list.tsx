"use client";

import Link from "next/link";
import { useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormError, FormField } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { Secret } from "@/lib/types";
import { useCreateSecret, useDeleteSecret, useSecrets } from "./api";

export const SECRET_NAME_RE = /^[A-Za-z0-9/_+=.@-]{1,512}$/;

function CreateSecretDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [form, setForm] = useState({ name: "", description: "", value: "" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [serverError, setServerError] = useState<string | null>(null);
  const create = useCreateSecret();
  const close = (o: boolean) => {
    if (!o) {
      setForm({ name: "", description: "", value: "" });
      setErrors({});
      setServerError(null);
    }
    onOpenChange(o);
  };
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    if (!SECRET_NAME_RE.test(form.name)) errs.name = "1–512 characters: letters, digits and / _ + = . @ -";
    if (!form.value) errs.value = "Secret value is required.";
    setErrors(errs);
    setServerError(null);
    if (Object.keys(errs).length) return;
    try {
      await create.mutateAsync({ name: form.name, secret_string: form.value, ...(form.description ? { description: form.description } : {}) });
      close(false);
    } catch (err) {
      setServerError(errorMessage(err));
    }
  };
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Store a new secret</DialogTitle>
            <DialogDescription>Values are stored by the simulator and only returned through the audited “get value” call.</DialogDescription>
          </DialogHeader>
          <FormError message={serverError} />
          <FormField label="Secret name" required error={errors.name} hint="e.g. prod/db/password">
            {(p) => <Input {...p} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} autoFocus />}
          </FormField>
          <FormField label="Description">
            {(p) => <Input {...p} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />}
          </FormField>
          <FormField label="Secret value" required error={errors.value} hint="Plain text or JSON.">
            {(p) => <Textarea {...p} className="font-mono text-xs" rows={5} value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} />}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Store secret
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function SecretsList() {
  const list = useListParams({ sort: "name" });
  const q = useSecrets(list.params);
  const del = useDeleteSecret();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<string | null>(null);
  const columns: Column<Secret>[] = [
    {
      id: "name",
      header: "Secret name",
      sortable: true,
      cell: (r) => (
        <Link href={`/secrets/${encodeURIComponent(r.name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
          {r.name}
        </Link>
      ),
    },
    { id: "description", header: "Description", cell: (r) => <span className="text-muted-foreground">{r.description || "—"}</span> },
    { id: "updated_at", header: "Last changed", sortable: true, cell: (r) => <span className="text-xs">{formatDate(r.updated_at ?? r.created_at)}</span> },
    { id: "created_at", header: "Created", sortable: true, cell: (r) => <span className="text-xs">{formatDate(r.created_at)}</span> },
    {
      id: "actions",
      header: "",
      className: "text-right",
      cell: (r) => (
        <Button size="icon-sm" variant="ghost" aria-label={`Delete secret ${r.name}`} onClick={() => setToDelete(r.name)}>
          <Trash2 />
        </Button>
      ),
    },
  ];
  return (
    <div className="grid gap-4">
      <PageHeader
        title="Secrets Manager"
        description="Store simulated credentials and configuration values. Reading a value is an audited action (secretsmanager:GetSecretValue)."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> Store a new secret
          </Button>
        }
      />
      <ResourceTable<Secret>
        label="Secrets"
        columns={columns}
        getRowId={(r) => r.name}
        data={q.data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        emptyTitle="No secrets"
        emptyDescription="Store a secret to reference it from labs or the CLI."
      />
      <CreateSecretDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete secret ${toDelete ?? ""}?`}
        destructive
        confirmLabel="Delete secret"
        typeToConfirm={toDelete ?? undefined}
        description="The secret and its value are deleted from the simulator."
        onConfirm={() => del.mutateAsync(toDelete!)}
      />
    </div>
  );
}
