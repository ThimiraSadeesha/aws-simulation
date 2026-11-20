"use client";

import Link from "next/link";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { JsonEditor, prettyJson } from "@/components/console/json-editor";
import { ResourceTable } from "@/components/console/resource-table";
import { Mono } from "@/components/console/copy-button";
import { api, errorMessage } from "@/lib/api/client";
import type { AttachedPolicy, PolicyDocument } from "@/lib/types";
import { parsePolicyJson, validateIamName } from "@/lib/validation";
import {
  attachedName,
  inlineNames,
  useAllIamPolicies,
  useAttachPolicy,
  useDeleteInlinePolicy,
  useDetachPolicy,
  usePutInlinePolicy,
} from "../api";
import { DEFAULT_POLICY, type PrincipalKind } from "../types";

interface Props {
  kind: PrincipalKind;
  name: string;
  attached: AttachedPolicy[] | undefined;
  inline: (string | { policy_name: string; policy_document?: PolicyDocument })[] | undefined;
}

/** Managed (attached) and inline policies of a user, group or role. */
export function PermissionsPanel({ kind, name, attached, inline }: Props) {
  const [attachOpen, setAttachOpen] = useState(false);
  const [detach, setDetach] = useState<string | null>(null);
  const [editInline, setEditInline] = useState<{ name: string; isNew: boolean } | null>(null);
  const [deleteInline, setDeleteInline] = useState<string | null>(null);
  const detachM = useDetachPolicy();
  const delInline = useDeleteInlinePolicy();
  const attachedRows = (attached ?? []).map((p) => ({ name: attachedName(p), arn: typeof p === "string" ? "" : (p.policy_arn ?? p.arn ?? "") }));
  const inlineRows = inlineNames(inline).map((n) => ({ name: n }));

  return (
    <div className="grid gap-4">
      <Section
        title="Attached managed policies"
        actions={
          <Button size="sm" variant="outline" onClick={() => setAttachOpen(true)}>
            <Plus /> Attach policy
          </Button>
        }
      >
        <ResourceTable
          label="Attached managed policies"
          data={attachedRows}
          getRowId={(r) => r.name}
          globalSearch={false}
          hideSearch
          emptyTitle="No managed policies attached"
          columns={[
            {
              id: "name",
              header: "Policy name",
              sortable: true,
              cell: (r) => (
                <Link className="text-sky-700 hover:underline dark:text-sky-400" href={`/iam/policies/${encodeURIComponent(r.name)}`}>
                  {r.name}
                </Link>
              ),
            },
            { id: "arn", header: "ARN", cell: (r) => <Mono copy>{r.arn}</Mono> },
            {
              id: "actions",
              header: "",
              className: "text-right",
              cell: (r) => (
                <Button size="xs" variant="ghost" onClick={() => setDetach(r.name)} aria-label={`Detach ${r.name}`}>
                  Detach
                </Button>
              ),
            },
          ]}
        />
      </Section>

      <Section
        title="Inline policies"
        actions={
          <Button size="sm" variant="outline" onClick={() => setEditInline({ name: "", isNew: true })}>
            <Plus /> Add inline policy
          </Button>
        }
      >
        <ResourceTable
          label="Inline policies"
          data={inlineRows}
          getRowId={(r) => r.name}
          globalSearch={false}
          hideSearch
          emptyTitle="No inline policies"
          columns={[
            { id: "name", header: "Policy name", sortable: true, cell: (r) => r.name },
            {
              id: "actions",
              header: "",
              className: "text-right",
              cell: (r) => (
                <div className="flex justify-end gap-1">
                  <Button size="xs" variant="ghost" onClick={() => setEditInline({ name: r.name, isNew: false })} aria-label={`Edit ${r.name}`}>
                    <Pencil /> Edit
                  </Button>
                  <Button size="xs" variant="ghost" onClick={() => setDeleteInline(r.name)} aria-label={`Delete ${r.name}`}>
                    <Trash2 /> Delete
                  </Button>
                </div>
              ),
            },
          ]}
        />
      </Section>

      <AttachPolicyDialog open={attachOpen} onOpenChange={setAttachOpen} kind={kind} name={name} exclude={attachedRows.map((r) => r.name)} />
      {editInline && (
        <InlinePolicyDialog
          kind={kind}
          name={name}
          policyName={editInline.name}
          isNew={editInline.isNew}
          inline={inline}
          onClose={() => setEditInline(null)}
        />
      )}
      <ConfirmDialog
        open={!!detach}
        onOpenChange={(o) => !o && setDetach(null)}
        title={`Detach ${detach}?`}
        description={`The policy will no longer grant permissions to ${name}.`}
        confirmLabel="Detach"
        destructive
        onConfirm={() => detachM.mutateAsync({ kind, name, policy: detach! })}
      />
      <ConfirmDialog
        open={!!deleteInline}
        onOpenChange={(o) => !o && setDeleteInline(null)}
        title={`Delete inline policy ${deleteInline}?`}
        confirmLabel="Delete"
        destructive
        onConfirm={() => delInline.mutateAsync({ kind, name, policy: deleteInline! })}
      />
    </div>
  );
}

function AttachPolicyDialog({
  open,
  onOpenChange,
  kind,
  name,
  exclude,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  kind: PrincipalKind;
  name: string;
  exclude: string[];
}) {
  const policies = useAllIamPolicies();
  const attach = useAttachPolicy();
  const [selected, setSelected] = useState("");
  const options = (policies.data ?? []).filter((p) => !exclude.includes(p.policy_name));
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Attach managed policy</DialogTitle>
          <DialogDescription>Attach a built-in or customer managed policy to {name}.</DialogDescription>
        </DialogHeader>
        <FormField label="Policy" required>
          {(p) => (
            <NativeSelect {...p} value={selected} onChange={(e) => setSelected(e.target.value)} disabled={policies.isLoading}>
              <option value="">{policies.isLoading ? "Loading policies…" : "Select a policy"}</option>
              {options.map((o) => (
                <option key={o.policy_name} value={o.policy_name}>
                  {o.policy_name}
                  {o.description ? ` — ${o.description}` : ""}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        {policies.isError && <FormError message={errorMessage(policies.error)} />}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={!selected || attach.isPending}
            onClick={async () => {
              await attach.mutateAsync({ kind, name, policy: selected });
              setSelected("");
              onOpenChange(false);
            }}
          >
            {attach.isPending && <Loader2 className="animate-spin" />} Attach
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function InlinePolicyDialog({
  kind,
  name,
  policyName,
  isNew,
  inline,
  onClose,
}: {
  kind: PrincipalKind;
  name: string;
  policyName: string;
  isNew: boolean;
  inline: Props["inline"];
  onClose: () => void;
}) {
  const existing = (inline ?? []).find((p) => typeof p !== "string" && p.policy_name === policyName);
  const initialDoc = existing && typeof existing !== "string" && existing.policy_document ? existing.policy_document : null;
  const [pName, setPName] = useState(policyName);
  const [text, setText] = useState(prettyJson(initialDoc ?? DEFAULT_POLICY));
  const needsFetch = !isNew && !initialDoc;
  const remote = useQuery({
    queryKey: ["iam", kind, name, "inline-policy", policyName],
    enabled: needsFetch,
    queryFn: ({ signal }) =>
      api<unknown>(`/iam/${kind}/${encodeURIComponent(name)}/inline-policies/${encodeURIComponent(policyName)}`, { signal }),
  });
  const [appliedRemote, setAppliedRemote] = useState(false);
  if (needsFetch && remote.data !== undefined && !appliedRemote) {
    // Adjust state from fetched data (render-phase update, no effect needed).
    setAppliedRemote(true);
    const d = remote.data as { policy_document?: unknown } | null;
    setText(prettyJson(d && typeof d === "object" && "policy_document" in d ? d.policy_document : d));
  }
  const loadError = remote.isError ? errorMessage(remote.error) : null;
  const [error, setError] = useState<string | null>(null);
  const put = usePutInlinePolicy();

  const save = async () => {
    setError(null);
    const nameErr = validateIamName(pName, "Policy name");
    if (nameErr) return setError(nameErr);
    const parsed = parsePolicyJson(text);
    if (!parsed.value) return setError(parsed.error);
    try {
      await put.mutateAsync({ kind, name, policy: pName, document: parsed.value });
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{isNew ? "Add inline policy" : `Edit inline policy ${policyName}`}</DialogTitle>
          <DialogDescription>Inline policies are embedded in {name} and deleted with it.</DialogDescription>
        </DialogHeader>
        <FormError message={error ?? (loadError ? `Could not load the current document: ${loadError}` : null)} />
        <FormField label="Policy name" required>
          {(p) => <Input {...p} value={pName} disabled={!isNew} onChange={(e) => setPName(e.target.value)} />}
        </FormField>
        <JsonEditor label="Inline policy document" value={text} onChange={setText} validatePolicy height={300} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void save()} disabled={put.isPending}>
            {put.isPending && <Loader2 className="animate-spin" />} Save policy
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
