"use client";

import { useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { FormError, FormField } from "@/components/console/form-field";
import { SecretOnceDialog } from "@/components/console/secret-once-dialog";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import { KEY_PAIR_NAME_RE } from "@/lib/validation";
import type { CreatedKeyPair, KeyPair } from "@/lib/types";
import { useCreateKeyPair, useDeleteKeyPair, useKeyPairs } from "@/features/ec2/api";

export default function KeyPairsPage() {
  const list = useListParams({ sort: "name" });
  const q = useKeyPairs(list.params);
  const create = useCreateKeyPair();
  const del = useDeleteKeyPair();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [nameErr, setNameErr] = useState<string | null>(null);
  const [formErr, setFormErr] = useState<string | null>(null);
  const [created, setCreated] = useState<CreatedKeyPair | null>(null);
  const [toDelete, setToDelete] = useState<KeyPair[]>([]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormErr(null);
    if (!KEY_PAIR_NAME_RE.test(name.trim())) {
      setNameErr("1–255 characters: letters, digits, spaces and . - _ @");
      return;
    }
    setNameErr(null);
    try {
      const kp = await create.mutateAsync(name.trim());
      setCreateOpen(false);
      setName("");
      setCreated(kp);
      toast.success(`Key pair ${kp.name} created`);
    } catch (err) {
      setFormErr(errorMessage(err));
    }
  };

  return (
    <div>
      <PageHeader
        title="Key pairs"
        description="SSH-style key pairs for simulated instances. The private key is shown only once at creation."
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create key pair
          </Button>
        }
      />
      <ResourceTable<KeyPair>
        label="Key pairs"
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(k) => k.name}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: (rows) => setToDelete(rows) }]}
        columns={[
          { id: "name", header: "Name", sortable: true, cell: (k) => <span className="font-medium">{k.name}</span> },
          { id: "key_pair_id", header: "ID", cell: (k) => <Mono copy>{k.key_pair_id}</Mono> },
          { id: "fingerprint", header: "Fingerprint", cell: (k) => <Mono>{k.fingerprint}</Mono> },
          { id: "created_at", header: "Created", sortable: true, cell: (k) => <span className="text-xs">{formatDate(k.created_at)}</span> },
          {
            id: "actions",
            header: "",
            cell: (k) => (
              <Button size="icon-xs" variant="ghost" aria-label={`Delete key pair ${k.name}`} onClick={() => setToDelete([k])}>
                <Trash2 />
              </Button>
            ),
          },
        ]}
        emptyTitle="No key pairs"
        emptyDescription="Create a key pair to attach to instances at launch."
      />

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <form onSubmit={submit} noValidate className="grid gap-4">
            <DialogHeader>
              <DialogTitle>Create key pair</DialogTitle>
              <DialogDescription>The private key (PEM) will be displayed once. Download and store it safely.</DialogDescription>
            </DialogHeader>
            <FormError message={formErr} />
            <FormField label="Key pair name" required error={nameErr}>
              {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
            </FormField>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={create.isPending}>
                {create.isPending && <Loader2 className="animate-spin" />} Create key pair
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <SecretOnceDialog
        open={!!created}
        onClose={() => setCreated(null)}
        title={`Key pair ${created?.name ?? ""} created`}
        fields={
          created
            ? [
                { label: "Fingerprint", value: created.fingerprint },
                { label: "Private key", value: created.private_key_pem, multiline: true },
              ]
            : []
        }
        download={created ? { filename: `${created.name}.pem`, content: created.private_key_pem } : undefined}
      />

      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length === 1 ? `key pair ${toDelete[0]?.name}` : `${toDelete.length} key pairs`}?`}
        description="Instances launched with this key keep running, but the key can no longer be selected."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const k of toDelete) await del.mutateAsync(k.name);
          toast.success(`Deleted ${toDelete.length} key pair${toDelete.length === 1 ? "" : "s"}`);
        }}
      />
    </div>
  );
}
