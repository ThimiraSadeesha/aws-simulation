"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { KeyRound, Loader2, Plus, Trash2, UserMinus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { SecretOnceDialog } from "@/components/console/secret-once-dialog";
import { StatusBadge } from "@/components/console/status-badge";
import { ErrorState, LoadingState } from "@/components/console/states";
import { formatDate, tagsToString } from "@/lib/format";
import type { AccessKey, CreatedAccessKey } from "@/lib/types";
import {
  groupNames,
  useAccessKeys,
  useAddUserToGroup,
  useAllIamGroups,
  useCreateAccessKey,
  useDeleteAccessKey,
  useDeleteUser,
  useIamUser,
  useRemoveUserFromGroup,
  useUpdateAccessKey,
} from "../api";
import { PermissionsPanel } from "./permissions-panel";

export function UserDetail({ name }: { name: string }) {
  const router = useRouter();
  const q = useIamUser(name);
  const del = useDeleteUser();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const crumbs = [{ label: "IAM", href: "/iam" }, { label: "Users", href: "/iam/users" }, { label: name }];

  if (q.isLoading) return <LoadingState />;
  if (q.isError || !q.data)
    return (
      <div>
        <PageHeader title={name} breadcrumbs={crumbs} />
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      </div>
    );
  const u = q.data;

  return (
    <div className="grid gap-4">
      <PageHeader
        title={u.user_name}
        breadcrumbs={crumbs}
        actions={
          <Button variant="destructive" size="sm" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete user
          </Button>
        }
      />
      <Section title="Summary">
        <DetailGrid
          items={[
            { label: "ARN", value: <Mono copy>{u.arn}</Mono> },
            { label: "User ID", value: <Mono>{u.user_id}</Mono> },
            { label: "Path", value: <span className="font-mono text-xs">{u.path || "/"}</span> },
            { label: "Created", value: formatDate(u.created_at) },
            { label: "Tags", value: tagsToString(u.tags) || "—" },
          ]}
        />
      </Section>
      <Tabs defaultValue="permissions">
        <TabsList>
          <TabsTrigger value="permissions">Permissions</TabsTrigger>
          <TabsTrigger value="groups">Groups</TabsTrigger>
          <TabsTrigger value="keys">Access keys</TabsTrigger>
        </TabsList>
        <TabsContent value="permissions" className="mt-3">
          <PermissionsPanel kind="users" name={u.user_name} attached={u.attached_policies} inline={u.inline_policies} />
        </TabsContent>
        <TabsContent value="groups" className="mt-3">
          <UserGroups user={u.user_name} groups={groupNames(u.groups)} />
        </TabsContent>
        <TabsContent value="keys" className="mt-3">
          <AccessKeys user={u.user_name} fallback={u.access_keys} />
        </TabsContent>
      </Tabs>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete user ${u.user_name}?`}
        description="The user's access keys, group memberships and inline policies are removed. This cannot be undone."
        confirmLabel="Delete user"
        destructive
        typeToConfirm={u.user_name}
        onConfirm={async () => {
          await del.mutateAsync(u.user_name);
          router.push("/iam/users");
        }}
      />
    </div>
  );
}

function UserGroups({ user, groups }: { user: string; groups: string[] }) {
  const all = useAllIamGroups();
  const add = useAddUserToGroup();
  const remove = useRemoveUserFromGroup();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const [toRemove, setToRemove] = useState<string | null>(null);
  const options = (all.data ?? []).filter((g) => !groups.includes(g.group_name));

  return (
    <Section
      title="Group memberships"
      actions={
        <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
          <Plus /> Add to group
        </Button>
      }
    >
      <ResourceTable
        label="Groups of this user"
        data={groups.map((g) => ({ name: g }))}
        getRowId={(r) => r.name}
        hideSearch
        globalSearch={false}
        emptyTitle="Not a member of any group"
        columns={[
          {
            id: "name",
            header: "Group",
            sortable: true,
            cell: (r) => (
              <Link href={`/iam/groups/${encodeURIComponent(r.name)}`} className="text-sky-700 hover:underline dark:text-sky-400">
                {r.name}
              </Link>
            ),
          },
          {
            id: "a",
            header: "",
            className: "text-right",
            cell: (r) => (
              <Button size="xs" variant="ghost" aria-label={`Remove from ${r.name}`} onClick={() => setToRemove(r.name)}>
                <UserMinus /> Remove
              </Button>
            ),
          },
        ]}
      />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add {user} to a group</DialogTitle>
            <DialogDescription>The user inherits the group&apos;s policies.</DialogDescription>
          </DialogHeader>
          <FormField label="Group" required>
            {(p) => (
              <NativeSelect {...p} value={selected} onChange={(e) => setSelected(e.target.value)}>
                <option value="">{all.isLoading ? "Loading…" : options.length ? "Select a group" : "No other groups available"}</option>
                {options.map((g) => (
                  <option key={g.group_name} value={g.group_name}>
                    {g.group_name}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              disabled={!selected || add.isPending}
              onClick={async () => {
                await add.mutateAsync({ group: selected, user });
                setSelected("");
                setOpen(false);
              }}
            >
              {add.isPending && <Loader2 className="animate-spin" />} Add to group
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={!!toRemove}
        onOpenChange={(o) => !o && setToRemove(null)}
        title={`Remove ${user} from ${toRemove}?`}
        confirmLabel="Remove"
        destructive
        onConfirm={() => remove.mutateAsync({ group: toRemove!, user })}
      />
    </Section>
  );
}

function AccessKeys({ user, fallback }: { user: string; fallback?: AccessKey[] }) {
  const keys = useAccessKeys(user);
  const create = useCreateAccessKey();
  const update = useUpdateAccessKey();
  const del = useDeleteAccessKey();
  const [created, setCreated] = useState<CreatedAccessKey | null>(null);
  const [toDelete, setToDelete] = useState<string | null>(null);
  const data = keys.data ?? (keys.isError ? fallback : undefined);

  return (
    <Section
      title="Access keys"
      actions={
        <Button
          size="sm"
          disabled={create.isPending}
          onClick={async () => {
            const k = await create.mutateAsync(user);
            setCreated(k);
          }}
        >
          {create.isPending ? <Loader2 className="animate-spin" /> : <KeyRound />} Create access key
        </Button>
      }
    >
      <p className="mb-3 text-xs text-muted-foreground">
        Access keys sign CLI / programmatic requests: <code className="font-mono">Authorization: CloudSim &lt;access_key_id&gt;:&lt;secret&gt;</code>. The secret is
        shown only once at creation.
      </p>
      <ResourceTable
        label="Access keys"
        data={data}
        isLoading={keys.isLoading}
        error={data ? undefined : keys.error}
        onRetry={() => void keys.refetch()}
        getRowId={(k) => k.access_key_id}
        hideSearch
        globalSearch={false}
        emptyTitle="No access keys"
        columns={[
          { id: "access_key_id", header: "Access key ID", sortable: true, cell: (k) => <Mono copy>{k.access_key_id}</Mono> },
          { id: "status", header: "Status", sortable: true, cell: (k) => <StatusBadge status={k.status} /> },
          { id: "created_at", header: "Created", sortable: true, cell: (k) => formatDate(k.created_at) },
          { id: "last_used_at", header: "Last used", cell: (k) => (k.last_used_at ? formatDate(k.last_used_at) : "Never") },
          {
            id: "actions",
            header: "",
            className: "text-right",
            cell: (k) => (
              <div className="flex justify-end gap-1">
                <Button
                  size="xs"
                  variant="outline"
                  disabled={update.isPending}
                  onClick={() => update.mutate({ user, keyId: k.access_key_id, status: k.status === "Active" ? "Inactive" : "Active" })}
                >
                  {k.status === "Active" ? "Deactivate" : "Activate"}
                </Button>
                <Button size="xs" variant="ghost" aria-label={`Delete access key ${k.access_key_id}`} onClick={() => setToDelete(k.access_key_id)}>
                  <Trash2 />
                </Button>
              </div>
            ),
          },
        ]}
      />
      {created && (
        <SecretOnceDialog
          open
          onClose={() => setCreated(null)}
          title={`Access key created for ${user}`}
          fields={[
            { label: "Access key ID", value: created.access_key_id },
            { label: "Secret access key", value: created.secret_access_key },
          ]}
          download={{
            filename: `${user}_accessKeys.csv`,
            content: `User name,Access key ID,Secret access key\n${user},${created.access_key_id},${created.secret_access_key}\n`,
          }}
        />
      )}
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete access key ${toDelete}?`}
        description="Applications using this key will immediately lose access. This cannot be undone."
        confirmLabel="Delete key"
        destructive
        onConfirm={() => del.mutateAsync({ user, keyId: toDelete! })}
      />
    </Section>
  );
}
