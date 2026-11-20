"use client";

import { useState } from "react";
import { Loader2, Play, Plus, Save, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { JsonEditor, prettyJson } from "@/components/console/json-editor";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { ErrorState, LoadingState } from "@/components/console/states";
import { Mono } from "@/components/console/copy-button";
import { errorMessage } from "@/lib/api/client";
import { formatBytes, formatDate, formatNumber } from "@/lib/format";
import { parsePolicyJson } from "@/lib/validation";
import type { Bucket, LifecycleRule, PolicyDocument } from "@/lib/types";
import {
  useAbortMultipart,
  useBucketPolicy,
  useDeletePolicy,
  useLifecycle,
  useMultipartUploads,
  usePutLifecycle,
  usePutPolicy,
  useRunLifecycle,
  useSetAcl,
  useSetVersioning,
  type MultipartUpload,
} from "./api";

// ---------------- Properties ----------------
export function PropertiesTab({ bucket }: { bucket: Bucket }) {
  const versioning = useSetVersioning(bucket.name);
  const setAcl = useSetAcl(bucket.name);
  const [acl, setAclValue] = useState(bucket.acl);

  return (
    <div className="grid gap-4">
      <Section title="Bucket overview">
        <DetailGrid
          items={[
            { label: "Name", value: <Mono copy>{bucket.name}</Mono> },
            { label: "Region", value: bucket.region },
            { label: "Created", value: formatDate(bucket.created_at) },
            { label: "Objects", value: formatNumber(bucket.object_count) },
            { label: "Size", value: formatBytes(bucket.size_bytes) },
            { label: "ARN", value: <Mono copy>{`arn:cloudsim:s3:::${bucket.name}`}</Mono> },
          ]}
        />
      </Section>
      <Section title="Versioning">
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-sm">Status:</span>
          <StatusBadge status={bucket.versioning} />
          {bucket.versioning === "Enabled" ? (
            <Button size="sm" variant="outline" disabled={versioning.isPending} onClick={() => versioning.mutate("Suspended", { onSuccess: () => toast.success("Versioning suspended") })}>
              {versioning.isPending && <Loader2 className="animate-spin" />} Suspend versioning
            </Button>
          ) : (
            <Button size="sm" disabled={versioning.isPending} onClick={() => versioning.mutate("Enabled", { onSuccess: () => toast.success("Versioning enabled") })}>
              {versioning.isPending && <Loader2 className="animate-spin" />} Enable versioning
            </Button>
          )}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          Once enabled, versioning can only be suspended, not disabled. Existing versions are kept while suspended.
        </p>
      </Section>
      <Section title="Access control list (ACL)">
        <div className="flex flex-wrap items-end gap-3">
          <FormField label="Canned ACL" className="w-56">
            {(p) => (
              <NativeSelect {...p} value={acl} onChange={(e) => setAclValue(e.target.value as Bucket["acl"])}>
                <option value="private">private</option>
                <option value="public-read">public-read</option>
              </NativeSelect>
            )}
          </FormField>
          <Button size="sm" disabled={acl === bucket.acl || setAcl.isPending} onClick={() => setAcl.mutate(acl, { onSuccess: () => toast.success(`ACL set to ${acl}`) })}>
            {setAcl.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save ACL
          </Button>
        </div>
        {acl === "public-read" && <p className="mt-2 text-xs text-amber-700 dark:text-amber-400">public-read lets anonymous principals read objects in this (simulated) bucket.</p>}
      </Section>
    </div>
  );
}

// ---------------- Permissions (bucket policy) ----------------
function policyTemplate(bucket: string) {
  return prettyJson({
    Version: "2012-10-17",
    Statement: [
      {
        Sid: "AllowPublicRead",
        Effect: "Allow",
        Principal: "*",
        Action: ["s3:GetObject"],
        Resource: [`arn:cloudsim:s3:::${bucket}/*`],
      },
    ],
  });
}

function PolicyEditor({ bucket, initial }: { bucket: string; initial: PolicyDocument | null }) {
  const [text, setText] = useState(initial ? prettyJson(initial) : "");
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const put = usePutPolicy(bucket);
  const del = useDeletePolicy(bucket);
  const parsed = parsePolicyJson(text);

  const save = async () => {
    setError(null);
    if (parsed.error || !parsed.value) {
      setError(parsed.error ?? "Policy is empty.");
      return;
    }
    try {
      await put.mutateAsync(parsed.value);
      toast.success("Bucket policy saved");
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <Section
      title="Bucket policy"
      actions={
        <>
          {!initial && !text && (
            <Button size="sm" variant="outline" onClick={() => setText(policyTemplate(bucket))}>
              Start from template
            </Button>
          )}
          {initial && (
            <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
              <Trash2 /> Delete policy
            </Button>
          )}
          <Button size="sm" onClick={() => void save()} disabled={put.isPending || !text.trim()}>
            {put.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save policy
          </Button>
        </>
      }
    >
      <p className="mb-3 text-xs text-muted-foreground">
        {initial ? "This bucket has a policy." : "This bucket has no policy."} Bucket policies are evaluated together with IAM identity policies. Principal supports{" "}
        <code className="font-mono">&quot;*&quot;</code> and <code className="font-mono">{'{"CloudSim":"<arn>"}'}</code>.
      </p>
      <FormError message={error} />
      <div className="mt-2">
        <JsonEditor label="Bucket policy JSON" value={text} onChange={setText} validatePolicy height={360} />
      </div>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete bucket policy?"
        destructive
        confirmLabel="Delete policy"
        description="Access will then be governed by IAM identity policies and the bucket ACL only."
        onConfirm={async () => {
          await del.mutateAsync();
          setText("");
          toast.success("Bucket policy deleted");
        }}
      />
    </Section>
  );
}

export function PermissionsTab({ bucket }: { bucket: string }) {
  const q = useBucketPolicy(bucket);
  if (q.isLoading) return <LoadingState />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  return <PolicyEditor key={q.dataUpdatedAt} bucket={bucket} initial={q.data ?? null} />;
}

// ---------------- Lifecycle ----------------
const STORAGE_CLASSES = ["STANDARD_IA", "ONEZONE_IA", "INTELLIGENT_TIERING", "GLACIER_IR", "GLACIER", "DEEP_ARCHIVE"];

interface RuleDraft {
  id: string;
  status: "Enabled" | "Disabled";
  prefix: string;
  expiration_days: string;
  noncurrent_expiration_days: string;
  abort_incomplete_multipart_days: string;
  transitions: { days: string; storage_class: string }[];
}

const toDraft = (r: LifecycleRule): RuleDraft => ({
  id: r.id,
  status: r.status,
  prefix: r.prefix ?? "",
  expiration_days: r.expiration_days?.toString() ?? "",
  noncurrent_expiration_days: r.noncurrent_expiration_days?.toString() ?? "",
  abort_incomplete_multipart_days: r.abort_incomplete_multipart_days?.toString() ?? "",
  transitions: (r.transitions ?? []).map((t) => ({ days: String(t.days), storage_class: t.storage_class })),
});

const optInt = (s: string) => (s.trim() === "" ? undefined : Number(s));

export function validateRules(rules: RuleDraft[]): string | null {
  const ids = new Set<string>();
  for (const [i, r] of rules.entries()) {
    const n = `Rule ${i + 1}`;
    if (!r.id.trim()) return `${n}: ID is required.`;
    if (ids.has(r.id.trim())) return `${n}: duplicate rule ID "${r.id}".`;
    ids.add(r.id.trim());
    for (const [label, v] of [
      ["expiration days", r.expiration_days],
      ["noncurrent expiration days", r.noncurrent_expiration_days],
      ["abort incomplete multipart days", r.abort_incomplete_multipart_days],
    ] as const) {
      if (v.trim() !== "" && (!Number.isInteger(Number(v)) || Number(v) < 1)) return `${n}: ${label} must be a whole number ≥ 1.`;
    }
    for (const t of r.transitions) {
      if (!Number.isInteger(Number(t.days)) || Number(t.days) < 0 || t.days.trim() === "") return `${n}: transition days must be a whole number ≥ 0.`;
      if (!t.storage_class) return `${n}: choose a storage class for each transition.`;
    }
    if (!r.expiration_days && !r.noncurrent_expiration_days && !r.abort_incomplete_multipart_days && r.transitions.length === 0)
      return `${n}: add at least one action (expiration, transition or abort).`;
  }
  return null;
}

function LifecycleEditor({ bucket, initial }: { bucket: string; initial: LifecycleRule[] }) {
  const [rules, setRules] = useState<RuleDraft[]>(initial.map(toDraft));
  const [error, setError] = useState<string | null>(null);
  const put = usePutLifecycle(bucket);
  const run = useRunLifecycle(bucket);

  const update = (i: number, patch: Partial<RuleDraft>) => setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  const save = async () => {
    setError(null);
    const v = validateRules(rules);
    if (v) {
      setError(v);
      return;
    }
    const body: LifecycleRule[] = rules.map((r) => ({
      id: r.id.trim(),
      status: r.status,
      prefix: r.prefix,
      expiration_days: optInt(r.expiration_days),
      noncurrent_expiration_days: optInt(r.noncurrent_expiration_days),
      abort_incomplete_multipart_days: optInt(r.abort_incomplete_multipart_days),
      transitions: r.transitions.length ? r.transitions.map((t) => ({ days: Number(t.days), storage_class: t.storage_class })) : undefined,
    }));
    try {
      await put.mutateAsync(body);
      toast.success("Lifecycle configuration saved");
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <Section
      title="Lifecycle rules"
      actions={
        <>
          <Button
            size="sm"
            variant="outline"
            disabled={run.isPending}
            onClick={() => run.mutate(undefined, { onSuccess: () => toast.success("Lifecycle evaluation completed") })}
          >
            {run.isPending ? <Loader2 className="animate-spin" /> : <Play />} Run lifecycle now
          </Button>
          <Button size="sm" onClick={() => void save()} disabled={put.isPending}>
            {put.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save rules
          </Button>
        </>
      }
    >
      <p className="mb-3 text-xs text-muted-foreground">
        Rules are applied by a background job (every LIFECYCLE_INTERVAL, 60 s by default) or immediately with “Run lifecycle now”.
      </p>
      <FormError message={error} />
      <div className="mt-2 grid gap-3">
        {rules.length === 0 && <p className="text-sm text-muted-foreground">No lifecycle rules.</p>}
        {rules.map((r, i) => (
          <fieldset key={i} className="grid gap-3 rounded-md border p-3">
            <legend className="px-1 text-xs font-medium">Rule {i + 1}</legend>
            <div className="grid gap-3 sm:grid-cols-3">
              <FormField label="Rule ID" required>
                {(p) => <Input {...p} className="h-8" value={r.id} onChange={(e) => update(i, { id: e.target.value })} />}
              </FormField>
              <FormField label="Status">
                {(p) => (
                  <NativeSelect {...p} value={r.status} onChange={(e) => update(i, { status: e.target.value as RuleDraft["status"] })}>
                    <option value="Enabled">Enabled</option>
                    <option value="Disabled">Disabled</option>
                  </NativeSelect>
                )}
              </FormField>
              <FormField label="Key prefix" hint="Empty = whole bucket">
                {(p) => <Input {...p} className="h-8" value={r.prefix} onChange={(e) => update(i, { prefix: e.target.value })} />}
              </FormField>
              <FormField label="Expire current versions after (days)">
                {(p) => <Input {...p} className="h-8" type="number" min={1} value={r.expiration_days} onChange={(e) => update(i, { expiration_days: e.target.value })} />}
              </FormField>
              <FormField label="Expire noncurrent versions after (days)">
                {(p) => (
                  <Input {...p} className="h-8" type="number" min={1} value={r.noncurrent_expiration_days} onChange={(e) => update(i, { noncurrent_expiration_days: e.target.value })} />
                )}
              </FormField>
              <FormField label="Abort incomplete multipart after (days)">
                {(p) => (
                  <Input
                    {...p}
                    className="h-8"
                    type="number"
                    min={1}
                    value={r.abort_incomplete_multipart_days}
                    onChange={(e) => update(i, { abort_incomplete_multipart_days: e.target.value })}
                  />
                )}
              </FormField>
            </div>
            <div className="grid gap-2">
              <span className="text-xs font-medium">Transitions</span>
              {r.transitions.map((t, ti) => (
                <div key={ti} className="flex flex-wrap items-center gap-2">
                  <label className="flex items-center gap-1 text-xs">
                    After
                    <Input
                      aria-label={`Rule ${i + 1} transition ${ti + 1} days`}
                      type="number"
                      min={0}
                      className="h-8 w-20"
                      value={t.days}
                      onChange={(e) => update(i, { transitions: r.transitions.map((x, k) => (k === ti ? { ...x, days: e.target.value } : x)) })}
                    />
                    days move to
                  </label>
                  <NativeSelect
                    aria-label={`Rule ${i + 1} transition ${ti + 1} storage class`}
                    className="w-48"
                    value={t.storage_class}
                    onChange={(e) => update(i, { transitions: r.transitions.map((x, k) => (k === ti ? { ...x, storage_class: e.target.value } : x)) })}
                  >
                    {STORAGE_CLASSES.map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </NativeSelect>
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Remove rule ${i + 1} transition ${ti + 1}`}
                    onClick={() => update(i, { transitions: r.transitions.filter((_, k) => k !== ti) })}
                  >
                    <X />
                  </Button>
                </div>
              ))}
              <div className="flex gap-2">
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => update(i, { transitions: [...r.transitions, { days: "30", storage_class: "STANDARD_IA" }] })}
                >
                  <Plus /> Add transition
                </Button>
                <Button type="button" size="xs" variant="ghost" className="ml-auto text-destructive" onClick={() => setRules((rs) => rs.filter((_, j) => j !== i))}>
                  <Trash2 /> Remove rule
                </Button>
              </div>
            </div>
          </fieldset>
        ))}
        <div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() =>
              setRules((rs) => [
                ...rs,
                { id: `rule-${rs.length + 1}`, status: "Enabled", prefix: "", expiration_days: "", noncurrent_expiration_days: "", abort_incomplete_multipart_days: "", transitions: [] },
              ])
            }
          >
            <Plus /> Add rule
          </Button>
        </div>
      </div>
    </Section>
  );
}

export function LifecycleTab({ bucket }: { bucket: string }) {
  const q = useLifecycle(bucket);
  if (q.isLoading) return <LoadingState />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  return <LifecycleEditor key={q.dataUpdatedAt} bucket={bucket} initial={q.data ?? []} />;
}

// ---------------- Multipart uploads ----------------
export function MultipartTab({ bucket }: { bucket: string }) {
  const q = useMultipartUploads(bucket);
  const abort = useAbortMultipart(bucket);
  const [toAbort, setToAbort] = useState<MultipartUpload | null>(null);
  const columns: Column<MultipartUpload>[] = [
    { id: "key", header: "Key", sortable: true, cell: (r) => <span className="break-all">{r.key}</span> },
    { id: "upload_id", header: "Upload ID", cell: (r) => <Mono copy>{r.upload_id}</Mono> },
    { id: "parts", header: "Parts", cell: (r) => (r.parts ?? "—").toString() },
    { id: "initiated", header: "Initiated", sortable: true, cell: (r) => <span className="text-xs">{formatDate(r.initiated ?? r.created_at)}</span> },
    {
      id: "actions",
      header: "",
      className: "text-right",
      cell: (r) => (
        <Button size="sm" variant="ghost" onClick={() => setToAbort(r)} aria-label={`Abort upload ${r.upload_id}`}>
          <X /> Abort
        </Button>
      ),
    },
  ];
  return (
    <div className="grid gap-3">
      <p className="text-xs text-muted-foreground">
        Multipart uploads are started from the CLI / API (<code className="font-mono">cloudsim s3 …</code>). In-progress uploads consume storage until completed or aborted.
      </p>
      <ResourceTable<MultipartUpload>
        label="In-progress multipart uploads"
        columns={columns}
        getRowId={(r) => r.upload_id}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        globalSearch={false}
        emptyTitle="No in-progress multipart uploads"
      />
      <ConfirmDialog
        open={!!toAbort}
        onOpenChange={(o) => !o && setToAbort(null)}
        title="Abort multipart upload?"
        destructive
        confirmLabel="Abort upload"
        description={`Uploaded parts for ${toAbort?.key ?? ""} will be discarded.`}
        onConfirm={() => abort.mutateAsync(toAbort!.upload_id)}
      />
    </div>
  );
}
