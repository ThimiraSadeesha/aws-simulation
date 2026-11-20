"use client";

import { useParams, useRouter } from "next/navigation";
import { useState } from "react";
import { Eye, EyeOff, Loader2, Save, Trash2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { CopyButton, Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ErrorState, LoadingState } from "@/components/console/states";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import { useDeleteSecret, useRetrieveSecretValue, useSecret, useUpdateSecret } from "@/features/secrets/api";

export default function SecretDetailPage() {
  const params = useParams<{ name: string }>();
  const name = decodeURIComponent(params.name);
  const router = useRouter();
  const q = useSecret(name);
  const retrieve = useRetrieveSecretValue(name);
  const update = useUpdateSecret(name);
  const del = useDeleteSecret();
  const [revealed, setRevealed] = useState<string | null>(null);
  const [newValue, setNewValue] = useState("");
  const [updateError, setUpdateError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const save = async () => {
    setUpdateError(null);
    if (!newValue) {
      setUpdateError("Enter a new value.");
      return;
    }
    try {
      await update.mutateAsync({ secret_string: newValue });
      setNewValue("");
      setRevealed(null);
      toast.success("Secret value updated");
    } catch (err) {
      setUpdateError(errorMessage(err));
    }
  };

  return (
    <div className="grid gap-4">
      <PageHeader
        breadcrumbs={[{ label: "Secrets Manager", href: "/secrets" }, { label: name }]}
        title={name}
        actions={
          <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete
          </Button>
        }
      />
      {q.isLoading ? (
        <LoadingState />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      ) : q.data ? (
        <>
          <Section title="Secret details">
            <DetailGrid
              items={[
                { label: "Name", value: q.data.name },
                { label: "ARN", value: <Mono copy>{q.data.arn}</Mono> },
                { label: "Description", value: q.data.description || "—" },
                { label: "Version", value: <Mono>{q.data.version_id}</Mono> },
                { label: "Created", value: formatDate(q.data.created_at) },
                { label: "Last changed", value: formatDate(q.data.updated_at) },
              ]}
            />
          </Section>
          <Section
            title="Secret value"
            actions={
              revealed !== null ? (
                <Button size="sm" variant="outline" onClick={() => setRevealed(null)}>
                  <EyeOff /> Hide
                </Button>
              ) : (
                <Button
                  size="sm"
                  disabled={retrieve.isPending}
                  onClick={() => retrieve.mutate(undefined, { onSuccess: (v) => setRevealed(v) })}
                >
                  {retrieve.isPending ? <Loader2 className="animate-spin" /> : <Eye />} Retrieve secret value
                </Button>
              )
            }
          >
            <p className="mb-2 flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-400">
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
              Retrieving the value calls secretsmanager:GetSecretValue, which is recorded in the audit log.
            </p>
            {revealed === null ? (
              <p className="font-mono text-sm text-muted-foreground" aria-label="Secret value hidden">
                ••••••••••••
              </p>
            ) : (
              <div className="flex items-start gap-1">
                <pre className="max-h-64 flex-1 overflow-auto rounded-md border bg-muted/50 p-2 font-mono text-xs whitespace-pre-wrap" data-testid="secret-value">
                  {revealed}
                </pre>
                <CopyButton value={revealed} label="Copy secret value" />
              </div>
            )}
          </Section>
          <Section title="Update value">
            <div className="grid gap-3">
              <FormError message={updateError} />
              <FormField label="New secret value" hint="Replaces the current value (creates a new version).">
                {(p) => <Textarea {...p} rows={4} className="font-mono text-xs" value={newValue} onChange={(e) => setNewValue(e.target.value)} />}
              </FormField>
              <div>
                <Button size="sm" onClick={() => void save()} disabled={update.isPending}>
                  {update.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save new value
                </Button>
              </div>
            </div>
          </Section>
        </>
      ) : null}
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete secret ${name}?`}
        destructive
        confirmLabel="Delete secret"
        typeToConfirm={name}
        onConfirm={async () => {
          await del.mutateAsync(name);
          router.push("/secrets");
        }}
      />
    </div>
  );
}
