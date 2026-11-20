"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { FormError, FormField } from "@/components/console/form-field";
import { KeyValueEditor, kvToRecord, type KV } from "@/components/console/key-value-editor";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { IamUser } from "@/lib/types";
import { validateIamName, validateTags } from "@/lib/validation";
import { groupNames, useCreateUser, useDeleteUser, useIamUsers } from "../api";

export function UsersPage() {
  const list = useListParams({ sort: "user_name" });
  const q = useIamUsers(list.params);
  const del = useDeleteUser();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<IamUser[]>([]);

  return (
    <div>
      <PageHeader title="Users" description="IAM users are identities within your account that sign requests with access keys." />
      <ResourceTable
        label="IAM users"
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(u) => u.user_name}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create user
          </Button>
        }
        emptyTitle="No IAM users"
        emptyDescription="Create a user to give a person or application its own credentials."
        columns={[
          {
            id: "user_name",
            header: "User name",
            sortable: true,
            cell: (u) => (
              <Link href={`/iam/users/${encodeURIComponent(u.user_name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
                {u.user_name}
              </Link>
            ),
          },
          { id: "path", header: "Path", cell: (u) => <span className="font-mono text-xs">{u.path || "/"}</span> },
          { id: "groups", header: "Groups", cell: (u) => groupNames(u.groups).join(", ") || <span className="text-muted-foreground">—</span> },
          { id: "access_keys", header: "Access keys", cell: (u) => (u.access_keys ? u.access_keys.length : <span className="text-muted-foreground">—</span>) },
          { id: "arn", header: "ARN", cell: (u) => <Mono copy>{u.arn}</Mono> },
          { id: "created_at", header: "Created", sortable: true, cell: (u) => formatDate(u.created_at) },
        ]}
      />
      <CreateUserDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length === 1 ? `user ${toDelete[0]?.user_name}` : `${toDelete.length} users`}?`}
        description="Deleting a user removes its access keys, group memberships and inline policies. This cannot be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const u of toDelete) await del.mutateAsync(u.user_name);
        }}
      />
    </div>
  );
}

function CreateUserDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const router = useRouter();
  const create = useCreateUser();
  const [name, setName] = useState("");
  const [path, setPath] = useState("/");
  const [tags, setTags] = useState<KV[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [apiError, setApiError] = useState<string | null>(null);

  const reset = () => {
    setName("");
    setPath("/");
    setTags([]);
    setErrors({});
    setApiError(null);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiError(null);
    const errs: Record<string, string> = {};
    const n = validateIamName(name, "User name");
    if (n) errs.user_name = n;
    if (!/^\/([\w+=,.@-]+\/)*$/.test(path)) errs.path = "Path must start and end with '/', e.g. / or /engineering/.";
    const t = validateTags(tags);
    if (t) errs.tags = t;
    setErrors(errs);
    if (Object.keys(errs).length) return;
    try {
      const tagRec = kvToRecord(tags);
      await create.mutateAsync({ user_name: name, path, ...(Object.keys(tagRec).length ? { tags: tagRec } : {}) });
      onOpenChange(false);
      reset();
      router.push(`/iam/users/${encodeURIComponent(name)}`);
    } catch (err) {
      setApiError(errorMessage(err));
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
    >
      <DialogContent>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create IAM user</DialogTitle>
            <DialogDescription>The user has no permissions until you attach policies or add it to a group.</DialogDescription>
          </DialogHeader>
          <FormError message={apiError} />
          <FormField label="User name" required error={errors.user_name} hint="Up to 64 characters: letters, digits and + = , . @ _ -">
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
          </FormField>
          <FormField label="Path" error={errors.path}>
            {(p) => <Input {...p} value={path} onChange={(e) => setPath(e.target.value)} className="font-mono" />}
          </FormField>
          <div className="grid gap-1.5">
            <span className="text-xs font-medium">Tags</span>
            <KeyValueEditor value={tags} onChange={setTags} />
            {errors.tags && (
              <p role="alert" className="text-xs text-destructive">
                {errors.tags}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create user
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
