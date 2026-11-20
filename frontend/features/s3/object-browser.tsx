"use client";

import { useMemo, useRef, useState } from "react";
import { Download, File as FileIcon, Folder, FolderPlus, Info, Loader2, Trash2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { DetailGrid } from "@/components/console/detail-grid";
import { FormError, FormField } from "@/components/console/form-field";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { Mono } from "@/components/console/copy-button";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatBytes, formatDate } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { ListResponse, S3Object } from "@/lib/types";
import { objectDownloadUrl, useCreateFolder, useDeleteObject, useObjectInfo, useObjects, useUpload } from "./api";

type Row = { kind: "folder"; key: string } | { kind: "object"; key: string; obj: S3Object };

const rowName = (key: string, prefix: string) => key.slice(prefix.length) || key;

export function ObjectInfoDialog({
  bucket,
  objectKey,
  versionId,
  onClose,
}: {
  bucket: string;
  objectKey: string | null;
  versionId?: string;
  onClose: () => void;
}) {
  const info = useObjectInfo(bucket, objectKey, versionId);
  return (
    <Dialog open={!!objectKey} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="break-all">{objectKey}</DialogTitle>
          <DialogDescription>Object properties (GET object-info).</DialogDescription>
        </DialogHeader>
        {info.isLoading ? (
          <LoadingState />
        ) : info.isError ? (
          <ErrorState error={info.error} onRetry={() => void info.refetch()} compact />
        ) : info.data ? (
          <div className="grid gap-4">
            <DetailGrid
              columns={2}
              items={[
                { label: "Size", value: formatBytes(info.data.size) },
                { label: "Content type", value: info.data.content_type },
                { label: "ETag", value: <Mono copy>{info.data.etag}</Mono> },
                { label: "Last modified", value: formatDate(info.data.last_modified) },
                { label: "Storage class", value: info.data.storage_class },
                { label: "Version ID", value: <Mono copy>{info.data.version_id}</Mono> },
                { label: "Latest version", value: info.data.is_latest ? "Yes" : "No" },
                { label: "Delete marker", value: info.data.delete_marker ? "Yes" : "No" },
              ]}
            />
            <div>
              <h3 className="mb-1 text-xs font-medium text-muted-foreground">User metadata (x-amz-meta-*)</h3>
              {Object.keys(info.data.metadata ?? {}).length === 0 ? (
                <p className="text-sm text-muted-foreground">None</p>
              ) : (
                <table className="w-full text-xs">
                  <tbody>
                    {Object.entries(info.data.metadata).map(([k, v]) => (
                      <tr key={k} className="border-b last:border-0">
                        <td className="py-1 pr-3 font-mono">{k}</td>
                        <td className="py-1 break-all">{v}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        ) : null}
        <DialogFooter>
          {objectKey && (
            <Button variant="outline" asChild>
              <a href={objectDownloadUrl(bucket, objectKey, versionId)} download={objectKey.split("/").pop()}>
                <Download /> Download
              </a>
            </Button>
          )}
          <Button onClick={onClose}>Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CreateFolderDialog({ bucket, prefix, open, onOpenChange }: { bucket: string; prefix: string; open: boolean; onOpenChange: (o: boolean) => void }) {
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const create = useCreateFolder(bucket);
  const invalid = name.includes("/") ? "Folder names cannot contain '/'." : null;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!name.trim() || invalid) return;
    try {
      await create.mutateAsync(`${prefix}${name.trim()}/`);
      setName("");
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create folder</DialogTitle>
            <DialogDescription>
              Creates a zero-byte object <span className="font-mono">{prefix}&lt;name&gt;/</span>. Folders are just key prefixes.
            </DialogDescription>
          </DialogHeader>
          <FormError message={error} />
          <FormField label="Folder name" required error={invalid}>
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending || !name.trim() || !!invalid}>
              {create.isPending && <Loader2 className="animate-spin" />} Create folder
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function ObjectBrowser({ bucket }: { bucket: string }) {
  const [prefix, setPrefix] = useState("");
  const list = useListParams({ sort: "key" });
  const q = useObjects(bucket, { ...list.params, prefix });
  const upload = useUpload(bucket);
  const del = useDeleteObject(bucket);
  const [progress, setProgress] = useState<{ loaded: number; total: number } | null>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [uploadResult, setUploadResult] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const [folderOpen, setFolderOpen] = useState(false);
  const [infoKey, setInfoKey] = useState<string | null>(null);
  const [toDelete, setToDelete] = useState<string[] | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const goTo = (p: string) => {
    setPrefix(p);
    list.setPage(1);
  };

  const data: ListResponse<Row> | undefined = useMemo(() => {
    if (!q.data) return undefined;
    const folders: Row[] = list.state.page === 1 ? (q.data.common_prefixes ?? []).map((k) => ({ kind: "folder", key: k })) : [];
    // Hide the folder marker object for the current prefix itself.
    const objects: Row[] = q.data.items.filter((o) => o.key !== prefix).map((o) => ({ kind: "object", key: o.key, obj: o }));
    return { ...q.data, items: [...folders, ...objects], total: q.data.total + (q.data.common_prefixes?.length ?? 0) };
  }, [q.data, list.state.page, prefix]);

  const doUpload = async (files: File[]) => {
    if (files.length === 0) return;
    setUploadError(null);
    setUploadResult(null);
    setProgress({ loaded: 0, total: files.reduce((s, f) => s + f.size, 0) });
    try {
      const r = await upload.mutateAsync({ files, prefix, onProgress: setProgress });
      const n = r?.objects?.length ?? files.length;
      setUploadResult(`Uploaded ${n} object${n === 1 ? "" : "s"} to ${prefix || "/"}`);
    } catch (err) {
      setUploadError(errorMessage(err));
    } finally {
      setProgress(null);
      if (inputRef.current) inputRef.current.value = "";
    }
  };

  const crumbs = prefix.split("/").filter(Boolean);

  const columns: Column<Row>[] = [
    {
      id: "key",
      header: "Name",
      sortable: true,
      cell: (r) =>
        r.kind === "folder" ? (
          <button type="button" onClick={() => goTo(r.key)} className="inline-flex items-center gap-1.5 font-medium text-sky-700 hover:underline dark:text-sky-400">
            <Folder className="size-4" aria-hidden /> {rowName(r.key, prefix)}
          </button>
        ) : (
          <button
            type="button"
            onClick={() => setInfoKey(r.key)}
            className="inline-flex items-center gap-1.5 text-left hover:underline"
            aria-label={`Object ${rowName(r.key, prefix)} details`}
          >
            <FileIcon className="size-4 text-muted-foreground" aria-hidden />
            <span className="break-all">{rowName(r.key, prefix)}</span>
          </button>
        ),
    },
    { id: "size", header: "Size", sortable: true, cell: (r) => (r.kind === "object" ? formatBytes(r.obj.size) : "—") },
    { id: "content_type", header: "Type", cell: (r) => (r.kind === "object" ? <span className="text-xs">{r.obj.content_type}</span> : "Folder") },
    { id: "storage_class", header: "Storage class", cell: (r) => (r.kind === "object" ? <span className="font-mono text-xs">{r.obj.storage_class}</span> : "") },
    { id: "last_modified", header: "Last modified", sortable: true, cell: (r) => (r.kind === "object" ? <span className="text-xs">{formatDate(r.obj.last_modified)}</span> : "") },
    {
      id: "actions",
      header: "",
      className: "text-right whitespace-nowrap",
      cell: (r) =>
        r.kind === "object" ? (
          <span className="inline-flex gap-0.5">
            <Button size="icon-sm" variant="ghost" aria-label={`Properties of ${r.key}`} onClick={() => setInfoKey(r.key)}>
              <Info />
            </Button>
            <Button size="icon-sm" variant="ghost" asChild>
              <a href={objectDownloadUrl(bucket, r.key)} download={r.key.split("/").pop()} aria-label={`Download ${r.key}`}>
                <Download />
              </a>
            </Button>
            <Button size="icon-sm" variant="ghost" aria-label={`Delete ${r.key}`} onClick={() => setToDelete([r.key])}>
              <Trash2 />
            </Button>
          </span>
        ) : null,
    },
  ];

  return (
    <div className="grid gap-3">
      {/* Breadcrumb of prefixes */}
      <nav aria-label="Folder path" className="flex flex-wrap items-center gap-1 text-sm">
        <button type="button" onClick={() => goTo("")} className={cn("hover:underline", !prefix && "font-semibold")}>
          {bucket}
        </button>
        {crumbs.map((c, i) => {
          const p = `${crumbs.slice(0, i + 1).join("/")}/`;
          return (
            <span key={p} className="flex items-center gap-1">
              <span className="text-muted-foreground">/</span>
              <button type="button" onClick={() => goTo(p)} className={cn("hover:underline", i === crumbs.length - 1 && "font-semibold")}>
                {c}
              </button>
            </span>
          );
        })}
      </nav>

      {/* Upload drop zone */}
      <div
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragging(false);
          void doUpload(Array.from(e.dataTransfer.files));
        }}
        className={cn(
          "flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-4 text-center text-sm transition-colors",
          dragging ? "border-sky-500 bg-sky-500/5" : "border-border",
        )}
      >
        <Upload className="size-5 text-muted-foreground" aria-hidden />
        <p className="text-muted-foreground">
          Drag and drop files here to upload to <span className="font-mono">{prefix || "/"}</span>, or
        </p>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => inputRef.current?.click()} disabled={upload.isPending}>
            <Upload /> Choose files
          </Button>
          <Button size="sm" variant="outline" onClick={() => setFolderOpen(true)}>
            <FolderPlus /> Create folder
          </Button>
        </div>
        <input
          ref={inputRef}
          type="file"
          multiple
          className="sr-only"
          aria-label="Upload files"
          onChange={(e) => void doUpload(Array.from(e.target.files ?? []))}
        />
        {progress && (
          <div className="w-full max-w-md" aria-live="polite">
            <Progress value={progress.total ? (progress.loaded / progress.total) * 100 : 0} aria-label="Upload progress" />
            <p className="mt-1 text-xs text-muted-foreground">
              Uploading… {formatBytes(progress.loaded)} / {formatBytes(progress.total)}
            </p>
          </div>
        )}
        {uploadResult && (
          <p role="status" className="text-xs text-emerald-700 dark:text-emerald-400">
            {uploadResult}
          </p>
        )}
        {uploadError && <FormError message={uploadError} />}
      </div>

      <ResourceTable<Row>
        label="Objects"
        columns={columns}
        getRowId={(r) => `${r.kind}:${r.key}`}
        data={data}
        list={list}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        searchPlaceholder="Search keys in this folder"
        selectable
        bulkActions={[
          {
            label: "Delete",
            icon: <Trash2 />,
            destructive: true,
            disabled: (rows) => rows.every((r) => r.kind === "folder"),
            onClick: (rows) => setToDelete(rows.filter((r) => r.kind === "object").map((r) => r.key)),
          },
        ]}
        emptyTitle={prefix ? "This folder is empty" : "This bucket is empty"}
        emptyDescription="Upload files with the drop zone above."
      />

      <CreateFolderDialog bucket={bucket} prefix={prefix} open={folderOpen} onOpenChange={setFolderOpen} />
      <ObjectInfoDialog bucket={bucket} objectKey={infoKey} onClose={() => setInfoKey(null)} />
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={toDelete && toDelete.length > 1 ? `Delete ${toDelete.length} objects?` : `Delete ${toDelete?.[0] ?? ""}?`}
        destructive
        confirmLabel="Delete"
        description="In a versioned bucket this adds a delete marker (older versions are kept). Otherwise the object is permanently removed."
        onConfirm={async () => {
          for (const key of toDelete ?? []) await del.mutateAsync({ key });
        }}
      />
    </div>
  );
}
