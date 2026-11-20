"use client";

import { useParams, useRouter } from "next/navigation";
import { useState } from "react";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { PageHeader } from "@/components/console/page-header";
import { StatusBadge } from "@/components/console/status-badge";
import { ErrorState, LoadingState } from "@/components/console/states";
import { formatBytes, formatNumber } from "@/lib/format";
import { useBucket } from "@/features/s3/api";
import { DeleteBucketDialog } from "@/features/s3/bucket-list";
import { ObjectBrowser } from "@/features/s3/object-browser";
import { VersionsTab } from "@/features/s3/versions-tab";
import { LifecycleTab, MultipartTab, PermissionsTab, PropertiesTab } from "@/features/s3/settings-tabs";

export default function BucketPage() {
  const params = useParams<{ bucket: string }>();
  const name = decodeURIComponent(params.bucket);
  const router = useRouter();
  const q = useBucket(name);
  const [deleting, setDeleting] = useState<string | null>(null);

  return (
    <div>
      <PageHeader
        breadcrumbs={[{ label: "S3", href: "/s3" }, { label: "Buckets", href: "/s3" }, { label: name }]}
        title={name}
        description={
          q.data ? (
            <span className="inline-flex flex-wrap items-center gap-2">
              <span>{q.data.region}</span>·<span>{formatNumber(q.data.object_count)} objects</span>·<span>{formatBytes(q.data.size_bytes)}</span>·
              <span className="inline-flex items-center gap-1">
                Versioning <StatusBadge status={q.data.versioning} />
              </span>
            </span>
          ) : undefined
        }
        actions={
          <Button variant="destructive" size="sm" onClick={() => setDeleting(name)}>
            <Trash2 /> Delete bucket
          </Button>
        }
      />
      {q.isLoading ? (
        <LoadingState />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      ) : q.data ? (
        <Tabs defaultValue="objects">
          <TabsList className="flex-wrap">
            <TabsTrigger value="objects">Objects</TabsTrigger>
            <TabsTrigger value="versions">Versions</TabsTrigger>
            <TabsTrigger value="properties">Properties</TabsTrigger>
            <TabsTrigger value="permissions">Permissions</TabsTrigger>
            <TabsTrigger value="lifecycle">Lifecycle</TabsTrigger>
            <TabsTrigger value="multipart">Multipart uploads</TabsTrigger>
          </TabsList>
          <TabsContent value="objects" className="mt-3">
            <ObjectBrowser bucket={name} />
          </TabsContent>
          <TabsContent value="versions" className="mt-3">
            <VersionsTab bucket={name} versioning={q.data.versioning} />
          </TabsContent>
          <TabsContent value="properties" className="mt-3">
            <PropertiesTab key={`${q.data.acl}-${q.data.versioning}`} bucket={q.data} />
          </TabsContent>
          <TabsContent value="permissions" className="mt-3">
            <PermissionsTab bucket={name} />
          </TabsContent>
          <TabsContent value="lifecycle" className="mt-3">
            <LifecycleTab bucket={name} />
          </TabsContent>
          <TabsContent value="multipart" className="mt-3">
            <MultipartTab bucket={name} />
          </TabsContent>
        </Tabs>
      ) : null}
      <DeleteBucketDialog
        bucket={deleting}
        onDone={(deleted) => {
          setDeleting(null);
          if (deleted) router.push("/s3");
        }}
      />
    </div>
  );
}
