"use client";

import { useState } from "react";
import { DatabaseBackup, Loader2, ShieldCheck, Trash2, Zap } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { ResourceTable } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { ErrorState, LoadingState } from "@/components/console/states";
import { Mono } from "@/components/console/copy-button";
import { useAuth } from "@/features/auth/auth-context";
import { useAllInstances } from "@/features/ec2/api";
import { errorMessage } from "@/lib/api/client";
import { formatBytes, formatDate } from "@/lib/format";
import type { Backup, Fault, IntegrityResult, User } from "@/lib/types";
import {
  useAdminUsers,
  useBackups,
  useCreateBackup,
  useCreateFault,
  useDeleteFault,
  useEngineStats,
  useFaults,
  useIntegrityCheck,
  useRestoreBackup,
  useSetUserRole,
} from "./api";

function UsersSection() {
  const { user: me } = useAuth();
  const users = useAdminUsers(true);
  const setRole = useSetUserRole();
  return (
    <Section title="Platform users & roles">
      <ResourceTable<User>
        label="Platform users"
        data={users.data}
        isLoading={users.isLoading}
        isFetching={users.isFetching}
        error={users.error}
        onRetry={() => void users.refetch()}
        getRowId={(u) => String(u.id)}
        globalSearch={false}
        searchText={(u) => `${u.username} ${u.email} ${u.account_id} ${u.role}`}
        columns={[
          { id: "username", header: "Username", sortable: true, cell: (u) => <span className="font-medium">{u.username}</span> },
          { id: "email", header: "Email", sortable: true, cell: (u) => u.email || "—" },
          { id: "account_id", header: "Account", cell: (u) => <Mono copy>{u.account_id}</Mono> },
          { id: "created_at", header: "Created", sortable: true, cell: (u) => formatDate(u.created_at) },
          {
            id: "role",
            header: "Role",
            sortable: true,
            cell: (u) => (
              <NativeSelect
                aria-label={`Role for ${u.username}`}
                value={u.role}
                disabled={setRole.isPending || u.id === me?.id}
                title={u.id === me?.id ? "You cannot change your own role" : undefined}
                className="h-7 w-28"
                onChange={(e) =>
                  setRole.mutate(
                    { id: u.id, role: e.target.value as User["role"] },
                    { onSuccess: () => toast.success(`${u.username} is now ${e.target.value}`) },
                  )
                }
              >
                <option value="user">user</option>
                <option value="admin">admin</option>
              </NativeSelect>
            ),
          },
        ]}
      />
    </Section>
  );
}

function EngineSection() {
  const stats = useEngineStats(true);
  const integrity = useIntegrityCheck();
  const [result, setResult] = useState<IntegrityResult | null>(null);
  return (
    <Section
      title="Simulation engine & database"
      actions={
        <Button
          size="sm"
          variant="outline"
          disabled={integrity.isPending}
          onClick={() => integrity.mutate(undefined, { onSuccess: (r) => setResult(r) })}
        >
          {integrity.isPending ? <Loader2 className="animate-spin" /> : <ShieldCheck />} Run integrity check
        </Button>
      }
    >
      {result && (
        <div className="mb-4 flex flex-wrap items-center gap-2 rounded-md border p-3 text-sm" role="status">
          <span>Integrity check:</span>
          <StatusBadge status={result.integrity_check === "ok" ? "ok" : "failed"} label={result.integrity_check} />
          <span className="text-muted-foreground">
            {result.foreign_key_violations.length} foreign key violation{result.foreign_key_violations.length === 1 ? "" : "s"}
          </span>
          {result.foreign_key_violations.length > 0 && (
            <pre className="mt-2 w-full overflow-auto rounded bg-muted p-2 text-xs">{JSON.stringify(result.foreign_key_violations, null, 2)}</pre>
          )}
        </div>
      )}
      {stats.isLoading ? (
        <LoadingState />
      ) : stats.isError ? (
        <ErrorState error={stats.error} onRetry={() => void stats.refetch()} compact />
      ) : stats.data ? (
        <div className="grid gap-4">
          <DetailGrid
            columns={4}
            items={[
              { label: "Uptime", value: stats.data.uptime_seconds !== undefined ? `${Math.round(stats.data.uptime_seconds)} s` : undefined },
              { label: "Resources", value: stats.data.resources_total },
              { label: "Events emitted", value: stats.data.events_emitted },
              { label: "Pending transitions", value: stats.data.pending_transitions },
              { label: "Active faults", value: stats.data.active_faults },
              { label: "Ticks", value: stats.data.ticks },
              { label: "Time scale", value: stats.data.time_scale },
              { label: "Seed", value: stats.data.seed },
            ]}
          />
          {stats.data.resources && (
            <div>
              <h3 className="mb-1 text-xs font-medium text-muted-foreground">Resources by kind</h3>
              <div className="flex flex-wrap gap-2">
                {Object.entries(stats.data.resources).map(([k, v]) => (
                  <span key={k} className="rounded-md border px-2 py-0.5 text-xs">
                    {k}: <span className="font-semibold">{v}</span>
                  </span>
                ))}
              </div>
            </div>
          )}
          {stats.data.quotas !== undefined && (
            <div>
              <h3 className="mb-1 text-xs font-medium text-muted-foreground">Quotas</h3>
              <pre className="overflow-auto rounded-md bg-muted p-2 text-xs">{JSON.stringify(stats.data.quotas, null, 2)}</pre>
            </div>
          )}
        </div>
      ) : null}
    </Section>
  );
}

function BackupsSection() {
  const backups = useBackups(true);
  const create = useCreateBackup();
  const restore = useRestoreBackup();
  const [restoreFile, setRestoreFile] = useState<string | null>(null);
  return (
    <Section
      title="Backups"
      actions={
        <Button
          size="sm"
          disabled={create.isPending}
          onClick={() => create.mutate(undefined, { onSuccess: (b) => toast.success(`Backup created: ${b.file}`, { description: formatBytes(b.size) }) })}
        >
          {create.isPending ? <Loader2 className="animate-spin" /> : <DatabaseBackup />} Create backup
        </Button>
      }
    >
      <ResourceTable<Backup>
        label="Backups"
        data={backups.data}
        isLoading={backups.isLoading}
        isFetching={backups.isFetching}
        error={backups.error}
        onRetry={() => void backups.refetch()}
        getRowId={(b) => b.file}
        globalSearch={false}
        emptyTitle="No backups yet"
        emptyDescription="Backups are SQLite snapshots (VACUUM INTO) stored by the backend."
        columns={[
          { id: "file", header: "File", sortable: true, cell: (b) => <Mono>{b.file}</Mono> },
          { id: "size", header: "Size", sortable: true, cell: (b) => formatBytes(b.size) },
          { id: "created_at", header: "Created", sortable: true, cell: (b) => formatDate(b.created_at) },
          {
            id: "actions",
            header: "",
            cell: (b) => (
              <Button size="xs" variant="outline" onClick={() => setRestoreFile(b.file)}>
                Restore
              </Button>
            ),
          },
        ]}
      />
      <ConfirmDialog
        open={!!restoreFile}
        onOpenChange={(o) => !o && setRestoreFile(null)}
        title="Restore backup?"
        destructive
        confirmLabel="Restore"
        typeToConfirm="restore"
        description={
          <>
            The database will be replaced with <span className="font-mono">{restoreFile}</span> after an integrity check, and the backend
            reloads. All changes since the backup are lost for every user.
          </>
        }
        onConfirm={() => restore.mutateAsync(restoreFile!).then(() => toast.success("Backup restored"))}
      />
    </Section>
  );
}

function FaultsSection() {
  const faults = useFaults(true);
  const instances = useAllInstances();
  const create = useCreateFault();
  const del = useDeleteFault();
  const [form, setForm] = useState({ target: "", type: "crash" as Fault["type"], probability: "1", duration_ms: "" });
  const [error, setError] = useState<string | null>(null);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const probability = Number(form.probability);
    if (!(probability > 0 && probability <= 1)) return setError("Probability must be greater than 0 and at most 1.");
    const duration = form.duration_ms ? Number(form.duration_ms) : null;
    if (duration !== null && (!Number.isInteger(duration) || duration <= 0)) return setError("Duration must be a positive whole number of milliseconds.");
    create.mutate(
      { target: form.target || null, kind: form.target ? null : "instance", type: form.type, probability, duration_ms: duration },
      {
        onSuccess: (f) => toast.success(`Fault ${f?.id ?? ""} injected`),
        onError: (err) => setError(errorMessage(err)),
      },
    );
  };

  return (
    <Section title="Fault injection">
      <form onSubmit={submit} className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-5 lg:items-end" noValidate>
        <FormField label="Target instance" hint="Empty = any instance (kind: instance)">
          {(p) => (
            <NativeSelect {...p} value={form.target} onChange={(e) => setForm((f) => ({ ...f, target: e.target.value }))}>
              <option value="">Any instance</option>
              {(instances.data ?? []).map((i) => (
                <option key={i.instance_id} value={i.instance_id}>
                  {i.name ? `${i.name} (${i.instance_id})` : i.instance_id}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Fault type">
          {(p) => (
            <NativeSelect {...p} value={form.type} onChange={(e) => setForm((f) => ({ ...f, type: e.target.value as Fault["type"] }))}>
              <option value="crash">crash – running → stopped</option>
              <option value="degrade">degrade – fails LB health checks</option>
              <option value="start_failure">start_failure – next start fails</option>
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Probability (0–1)">
          {(p) => <Input {...p} inputMode="decimal" value={form.probability} onChange={(e) => setForm((f) => ({ ...f, probability: e.target.value }))} />}
        </FormField>
        <FormField label="Duration (ms)" hint="Empty = until removed">
          {(p) => <Input {...p} inputMode="numeric" value={form.duration_ms} onChange={(e) => setForm((f) => ({ ...f, duration_ms: e.target.value }))} />}
        </FormField>
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? <Loader2 className="animate-spin" /> : <Zap />} Inject fault
        </Button>
      </form>
      <FormError message={error} />
      <ResourceTable<Fault>
        label="Active faults"
        data={faults.data}
        isLoading={faults.isLoading}
        isFetching={faults.isFetching}
        error={faults.error}
        onRetry={() => void faults.refetch()}
        getRowId={(f) => f.id}
        globalSearch={false}
        emptyTitle="No active faults"
        columns={[
          { id: "id", header: "ID", sortable: true, cell: (f) => <Mono>{f.id}</Mono> },
          { id: "type", header: "Type", sortable: true, cell: (f) => f.type },
          { id: "target", header: "Target", cell: (f) => <Mono>{f.target ?? (f.kind ? `any ${f.kind}` : "any")}</Mono> },
          { id: "probability", header: "Probability", sortable: true, cell: (f) => f.probability },
          { id: "duration_ms", header: "Duration", cell: (f) => (f.duration_ms ? `${f.duration_ms} ms` : "until removed") },
          {
            id: "actions",
            header: "",
            cell: (f) => (
              <Button
                size="xs"
                variant="ghost"
                aria-label={`Remove fault ${f.id}`}
                disabled={del.isPending}
                onClick={() => del.mutate(f.id, { onSuccess: () => toast.success(`Fault ${f.id} cleared`) })}
              >
                <Trash2 /> Clear
              </Button>
            ),
          },
        ]}
      />
    </Section>
  );
}

export function AdminPanel() {
  return (
    <div className="grid gap-4">
      <UsersSection />
      <EngineSection />
      <BackupsSection />
      <FaultsSection />
    </div>
  );
}

