"use client";

import { useState } from "react";
import { Download, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { useDebounced } from "@/hooks/use-global-search";
import { formatBytes, formatDate } from "@/lib/format";
import type { S3Object } from "@/lib/types";
import { objectDownloadUrl, useDeleteObject, useVersions } from "./api";

export function VersionsTab({ bucket, versioning }: { bucket: string; versioning: string }) {
  const [prefixInput, setPrefixInput] = useState("");
  const prefix = useDebounced(prefixInput, 300);
  const q = useVersions(bucket, prefix);
  const del = useDeleteObject(bucket);
  const [toDelete, setToDelete] = useState<S3Object | null>(null);

  const columns: Column<S3Object>[] = [
    { id: "key", header: "Key", sortable: true, cell: (r) => <span className="break-all">{r.key}</span> },
    { id: "version_id", header: "Version ID", cell: (r) => <Mono copy>{r.version_id}</Mono> },
    {
      id: "is_latest",
      header: "Status",
      sortable: true,
      sortValue: (r) => (r.is_latest ? 0 : 1),
      cell: (r) => (
        <span className="inline-flex gap-1">
          {r.is_latest && <StatusBadge status="active" label="latest" />}
          {r.delete_marker && <StatusBadge status="deleted" label="delete marker" />}
        </span>
      ),
    },
    { id: "size", header: "Size", sortable: true, cell: (r) => (r.delete_marker ? "—" : formatBytes(r.size)) },
    { id: "last_modified", header: "Last modified", sortable: true, cell: (r) => <span className="text-xs">{formatDate(r.last_modified)}</span> },
    {
      id: "actions",
      header: "",
      className: "text-right whitespace-nowrap",
      cell: (r) => (
        <span className="inline-flex gap-0.5">
          {!r.delete_marker && (
            <Button size="icon-sm" variant="ghost" asChild>
              <a href={objectDownloadUrl(bucket, r.key, r.version_id)} download={r.key.split("/").pop()} aria-label={`Download version ${r.version_id} of ${r.key}`}>
                <Download />
              </a>
            </Button>
          )}
          <Button size="icon-sm" variant="ghost" aria-label={`Permanently delete version ${r.version_id} of ${r.key}`} onClick={() => setToDelete(r)}>
            <Trash2 />
          </Button>
        </span>
      ),
    },
  ];

  return (
    <div className="grid gap-3">
      {versioning === "Disabled" && (
        <p className="rounded-md border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
          Versioning is not enabled for this bucket, so each object has a single (null) version. Enable it on the Properties tab.
        </p>
      )}
      <div className="flex max-w-sm items-center gap-2">
        <label htmlFor="versions-prefix" className="text-xs whitespace-nowrap text-muted-foreground">
          Key prefix
        </label>
        <Input id="versions-prefix" value={prefixInput} onChange={(e) => setPrefixInput(e.target.value)} className="h-8" placeholder="e.g. photos/" />
      </div>
      <ResourceTable<S3Object>
        label="Object versions"
        columns={columns}
        getRowId={(r) => `${r.key}@${r.version_id}`}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        searchText={(r) => `${r.key} ${r.version_id}`}
        globalSearch={false}
        emptyTitle="No versions"
      />
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Permanently delete this version?"
        destructive
        confirmLabel="Delete version"
        description={
          <span>
            Version <span className="font-mono">{toDelete?.version_id}</span> of <span className="font-mono">{toDelete?.key}</span> will be removed permanently.
            {toDelete?.delete_marker && " Removing a delete marker restores the previous version."}
          </span>
        }
        onConfirm={() => del.mutateAsync({ key: toDelete!.key, versionId: toDelete!.version_id })}
      />
    </div>
  );
}
