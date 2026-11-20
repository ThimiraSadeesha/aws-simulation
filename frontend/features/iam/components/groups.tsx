"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, Plus, Trash2, UserMinus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { IamGroup } from "@/lib/types";
import { validateIamName } from "@/lib/validation";
import {
  groupMembers,
  useAddUserToGroup,
  useAllIamUsers,
  useCreateGroup,
  useDeleteGroup,
  useIamGroup,
  useIamGroups,
  useRemoveUserFromGroup,
} from "../api";
import { PermissionsPanel } from "./permissions-panel";

export function GroupsPage() {
  const list = useListParams({ sort: "group_name" });
  const q = useIamGroups(list.params);
  const del = useDeleteGroup();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<IamGroup[]>([]);
  return (
    <div>
      <PageHeader title="Groups" description="Groups let you grant the same permissions to several users." />
      <ResourceTable
        label="IAM groups"
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(g) => g.group_name}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create group
          </Button>
        }
        emptyTitle="No IAM groups"
        columns={[
          {
            id: "group_name",
            header: "Group name",
            sortable: true,
            cell: (g) => (
              <Link href={`/iam/groups/${encodeURIComponent(g.group_name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
                {g.group_name}
              </Link>
            ),
          },
          { id: "members", header: "Members", cell: (g) => (g.members || g.users ? groupMembers(g).length : "—") },
          { id: "arn", header: "ARN", cell: (g) => <Mono copy>{g.arn}</Mono> },
          { id: "created_at", header: "Created", sortable: true, cell: (g) => formatDate(g.created_at) },
        ]}
      />
      <CreateGroupDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length === 1 ? `group ${toDelete[0]?.group_name}` : `${toDelete.length} groups`}?`}
        description="Members lose the permissions granted by the group. This cannot be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const g of toDelete) await del.mutateAsync(g.group_name);
        }}
      />
    </div>
  );
}

function CreateGroupDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const create = useCreateGroup();
  const router = useRouter();
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [apiErr, setApiErr] = useState<string | null>(null);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    const v = validateIamName(name, "Group name");
    setErr(v);
    if (v) return;
    try {
      await create.mutateAsync({ group_name: name });
      onOpenChange(false);
      setName("");
      router.push(`/iam/groups/${encodeURIComponent(name)}`);
    } catch (e2) {
      setApiErr(errorMessage(e2));
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create group</DialogTitle>
            <DialogDescription>Attach policies to the group, then add users.</DialogDescription>
          </DialogHeader>
          <FormError message={apiErr} />
          <FormField label="Group name" required error={err}>
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create group
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function GroupDetail({ name }: { name: string }) {
  const router = useRouter();
  const q = useIamGroup(name);
  const users = useAllIamUsers();
  const add = useAddUserToGroup();
  const remove = useRemoveUserFromGroup();
  const del = useDeleteGroup();
  const [addOpen, setAddOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const [toRemove, setToRemove] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const crumbs = [{ label: "IAM", href: "/iam" }, { label: "Groups", href: "/iam/groups" }, { label: name }];

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data)
    return (
      <div>
        <PageHeader title={name} breadcrumbs={crumbs} />
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      </div>
    );
  const g = q.data;
  const members = groupMembers(g);
  const candidates = (users.data ?? []).filter((u) => !members.includes(u.user_name));

  return (
    <div className="grid gap-4">
      <PageHeader
        title={g.group_name}
        breadcrumbs={crumbs}
        actions={
          <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete group
          </Button>
        }
      />
      <Section title="Summary">
        <DetailGrid
          items={[
            { label: "ARN", value: <Mono copy>{g.arn}</Mono> },
            { label: "Members", value: members.length },
            { label: "Created", value: formatDate(g.created_at) },
          ]}
        />
      </Section>
      <Section
        title="Members"
        actions={
          <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
            <Plus /> Add users
          </Button>
        }
      >
        <ResourceTable
          label="Group members"
          data={members.map((m) => ({ name: m }))}
          getRowId={(r) => r.name}
          hideSearch
          globalSearch={false}
          emptyTitle="No members"
          columns={[
            {
              id: "name",
              header: "User",
              sortable: true,
              cell: (r) => (
                <Link href={`/iam/users/${encodeURIComponent(r.name)}`} className="text-sky-700 hover:underline dark:text-sky-400">
                  {r.name}
                </Link>
              ),
            },
            {
              id: "a",
              header: "",
              className: "text-right",
              cell: (r) => (
                <Button size="xs" variant="ghost" aria-label={`Remove ${r.name} from group`} onClick={() => setToRemove(r.name)}>
                  <UserMinus /> Remove
                </Button>
              ),
            },
          ]}
        />
      </Section>
      <PermissionsPanel kind="groups" name={g.group_name} attached={g.attached_policies} inline={g.inline_policies} />

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add user to {g.group_name}</DialogTitle>
          </DialogHeader>
          <FormField label="User" required>
            {(p) => (
              <NativeSelect {...p} value={selected} onChange={(e) => setSelected(e.target.value)}>
                <option value="">{users.isLoading ? "Loading…" : candidates.length ? "Select a user" : "No users available"}</option>
                {candidates.map((u) => (
                  <option key={u.user_name} value={u.user_name}>
                    {u.user_name}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddOpen(false)}>
              Cancel
            </Button>
            <Button
              disabled={!selected || add.isPending}
              onClick={async () => {
                await add.mutateAsync({ group: g.group_name, user: selected });
                setSelected("");
                setAddOpen(false);
              }}
            >
              {add.isPending && <Loader2 className="animate-spin" />} Add user
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={!!toRemove}
        onOpenChange={(o) => !o && setToRemove(null)}
        title={`Remove ${toRemove} from ${g.group_name}?`}
        confirmLabel="Remove"
        destructive
        onConfirm={() => remove.mutateAsync({ group: g.group_name, user: toRemove! })}
      />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete group ${g.group_name}?`}
        description="Members lose the permissions granted by the group."
        confirmLabel="Delete group"
        destructive
        onConfirm={async () => {
          await del.mutateAsync(g.group_name);
          router.push("/iam/groups");
        }}
      />
    </div>
  );
}
