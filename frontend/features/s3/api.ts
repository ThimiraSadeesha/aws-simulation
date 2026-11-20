"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiList, apiRaw, ApiError, encodeKey, toList, uploadForm, type UploadProgress } from "@/lib/api/client";
import type { Bucket, LifecycleRule, ListParams, ObjectListResponse, PolicyDocument, S3Object, S3Stats } from "@/lib/types";

const b = (bucket: string) => `/s3/buckets/${encodeURIComponent(bucket)}`;

export const s3Keys = {
  all: ["s3"] as const,
  buckets: (p?: ListParams) => ["s3", "buckets", p ?? {}] as const,
  bucket: (name: string) => ["s3", "bucket", name] as const,
  objects: (name: string, p: Record<string, unknown>) => ["s3", "bucket", name, "objects", p] as const,
  versions: (name: string, prefix: string) => ["s3", "bucket", name, "versions", prefix] as const,
  policy: (name: string) => ["s3", "bucket", name, "policy"] as const,
  lifecycle: (name: string) => ["s3", "bucket", name, "lifecycle"] as const,
  multipart: (name: string) => ["s3", "bucket", name, "multipart"] as const,
  objectInfo: (name: string, key: string, versionId?: string) => ["s3", "bucket", name, "object-info", key, versionId ?? ""] as const,
  stats: ["s3", "stats"] as const,
};

/** URL for downloading an object (cookie-authenticated GET – no CSRF needed). */
export function objectDownloadUrl(bucket: string, key: string, versionId?: string) {
  const q = versionId ? `?version_id=${encodeURIComponent(versionId)}` : "";
  return `/api/v1${b(bucket)}/objects/${encodeKey(key)}${q}`;
}

export function useBuckets(params: ListParams) {
  return useQuery({
    queryKey: s3Keys.buckets(params),
    queryFn: ({ signal }) => apiList<Bucket>("/s3/buckets", params, signal),
    placeholderData: keepPreviousData,
  });
}

export function useBucket(name: string) {
  return useQuery({
    queryKey: s3Keys.bucket(name),
    queryFn: ({ signal }) => api<Bucket>(b(name), { signal }),
    enabled: !!name,
  });
}

export function useS3Stats() {
  return useQuery({
    queryKey: s3Keys.stats,
    queryFn: ({ signal }) => api<S3Stats>("/s3/stats", { signal }),
  });
}

export function useObjects(bucket: string, params: ListParams & { prefix: string }) {
  const p = { ...params, delimiter: "/" };
  return useQuery({
    queryKey: s3Keys.objects(bucket, p),
    queryFn: async ({ signal }) => {
      const raw = await api<Partial<ObjectListResponse> | S3Object[]>(`${b(bucket)}/objects`, {
        signal,
        query: p as Record<string, string | number | undefined>,
      });
      const list = toList<S3Object>(raw, params);
      const common_prefixes = !Array.isArray(raw) && Array.isArray(raw?.common_prefixes) ? raw.common_prefixes : [];
      return { ...list, common_prefixes } as ObjectListResponse;
    },
    placeholderData: keepPreviousData,
    enabled: !!bucket,
  });
}

export function useVersions(bucket: string, prefix: string) {
  return useQuery({
    queryKey: s3Keys.versions(bucket, prefix),
    queryFn: async ({ signal }) =>
      toList<S3Object>(await api<unknown>(`${b(bucket)}/versions`, { signal, query: { prefix, page_size: 200 } })).items,
    enabled: !!bucket,
  });
}

export function useObjectInfo(bucket: string, key: string | null, versionId?: string) {
  return useQuery({
    queryKey: s3Keys.objectInfo(bucket, key ?? "", versionId),
    queryFn: ({ signal }) =>
      api<S3Object>(`${b(bucket)}/object-info/${encodeKey(key!)}`, { signal, query: { version_id: versionId } }),
    enabled: !!key,
  });
}

/** GET policy; a 404 NotFound means "no policy" and resolves to null. */
export function useBucketPolicy(bucket: string) {
  return useQuery({
    queryKey: s3Keys.policy(bucket),
    queryFn: async ({ signal }) => {
      try {
        const r = await api<PolicyDocument | { policy: PolicyDocument }>(`${b(bucket)}/policy`, { signal });
        if (!r) return null;
        // Accept either the raw document or a {"policy": …} wrapper.
        if ("policy" in r && r.policy && typeof r.policy === "object" && !("Statement" in r)) return r.policy as PolicyDocument;
        return r as PolicyDocument;
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
      }
    },
  });
}

export function useLifecycle(bucket: string) {
  return useQuery({
    queryKey: s3Keys.lifecycle(bucket),
    queryFn: async ({ signal }) => {
      try {
        const r = await api<{ rules?: LifecycleRule[] }>(`${b(bucket)}/lifecycle`, { signal });
        return r?.rules ?? [];
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return [];
        throw err;
      }
    },
  });
}

export interface MultipartUpload {
  upload_id: string;
  key: string;
  initiated?: string;
  created_at?: string;
  parts?: number;
}

export function useMultipartUploads(bucket: string) {
  return useQuery({
    queryKey: s3Keys.multipart(bucket),
    queryFn: async ({ signal }) => toList<MultipartUpload>(await api<unknown>(`${b(bucket)}/multipart`, { signal })).items,
  });
}

// ---------------- mutations ----------------

function useInvalidateBucket(bucket?: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: bucket ? ["s3", "bucket", bucket] : s3Keys.all });
    void qc.invalidateQueries({ queryKey: ["s3", "buckets"] });
    void qc.invalidateQueries({ queryKey: s3Keys.stats });
    void qc.invalidateQueries({ queryKey: ["dashboard"] });
  };
}

export function useCreateBucket() {
  const inv = useInvalidateBucket();
  return useMutation({
    mutationFn: (body: { name: string; acl?: string }) => api<Bucket>("/s3/buckets", { method: "POST", body }),
    meta: { silent: true },
    onSuccess: inv,
  });
}

export function useDeleteBucket() {
  const inv = useInvalidateBucket();
  return useMutation({
    mutationFn: ({ name, force }: { name: string; force: boolean }) =>
      api<void>(b(name), { method: "DELETE", query: { force: force ? "true" : undefined } }),
    onSuccess: inv,
  });
}

export function useSetVersioning(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: (status: "Enabled" | "Suspended") => api<unknown>(`${b(bucket)}/versioning`, { method: "PUT", body: { status } }),
    onSuccess: inv,
  });
}

export function useSetAcl(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: (acl: "private" | "public-read") => api<unknown>(`${b(bucket)}/acl`, { method: "PUT", body: { acl } }),
    onSuccess: inv,
  });
}

export function usePutPolicy(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: (doc: PolicyDocument) => api<unknown>(`${b(bucket)}/policy`, { method: "PUT", body: doc }),
    meta: { silent: true },
    onSuccess: inv,
  });
}

export function useDeletePolicy(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: () => api<void>(`${b(bucket)}/policy`, { method: "DELETE" }),
    onSuccess: inv,
  });
}

export function usePutLifecycle(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: (rules: LifecycleRule[]) => api<unknown>(`${b(bucket)}/lifecycle`, { method: "PUT", body: { rules } }),
    meta: { silent: true },
    onSuccess: inv,
  });
}

export function useRunLifecycle(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: () => api<unknown>(`${b(bucket)}/lifecycle/run`, { method: "POST" }),
    onSuccess: inv,
  });
}

export function useUpload(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: ({ files, prefix, onProgress }: { files: File[]; prefix: string; onProgress?: (p: UploadProgress) => void }) => {
      const form = new FormData();
      for (const f of files) form.append("file", f, f.name);
      if (prefix) form.append("prefix", prefix);
      return uploadForm<{ objects: S3Object[] }>(`${b(bucket)}/upload`, form, onProgress);
    },
    meta: { silent: true },
    onSuccess: inv,
  });
}

/** Create a "folder" = zero-byte object whose key ends with "/". */
export function useCreateFolder(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: async (key: string) => {
      await apiRaw(`${b(bucket)}/objects/${encodeKey(key)}`, {
        method: "PUT",
        rawBody: new Blob([]),
        headers: { "Content-Type": "application/x-directory" },
      });
    },
    meta: { silent: true },
    onSuccess: inv,
  });
}

export function useDeleteObject(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: ({ key, versionId }: { key: string; versionId?: string }) =>
      api<void>(`${b(bucket)}/objects/${encodeKey(key)}`, { method: "DELETE", query: { version_id: versionId } }),
    onSuccess: inv,
  });
}

export function useAbortMultipart(bucket: string) {
  const inv = useInvalidateBucket(bucket);
  return useMutation({
    mutationFn: (uploadId: string) => api<void>(`${b(bucket)}/multipart/${encodeURIComponent(uploadId)}`, { method: "DELETE" }),
    onSuccess: inv,
  });
}
