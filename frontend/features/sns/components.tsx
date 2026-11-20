"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, Megaphone, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { useAllQueues } from "@/features/sqs/api";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { Topic } from "@/lib/types";
import { useCreateTopic, useDeleteTopic, useLogGroupOptions, usePublish, useSubscribe, useTopic, useTopics } from "./api";

export const TOPIC_NAME_RE = /^[A-Za-z0-9_-]{1,256}$/;

export function TopicsPage() {
  const list = useListParams({ sort: "name" });
  const q = useTopics(list.params);
  const del = useDeleteTopic();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<Topic[]>([]);
  return (
    <div>
      <PageHeader title="SNS topics" description="Publish a message once and fan it out to subscribed SQS queues and CloudWatch log groups." />
      <ResourceTable
        label="SNS topics"
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(t) => t.name}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create topic
          </Button>
        }
        emptyTitle="No topics"
        columns={[
          {
            id: "name",
            header: "Name",
            sortable: true,
            cell: (t) => (
              <Link href={`/sns/${encodeURIComponent(t.name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
                {t.name}
              </Link>
            ),
          },
          { id: "subscriptions", header: "Subscriptions", cell: (t) => t.subscriptions?.length ?? "—" },
          { id: "arn", header: "ARN", cell: (t) => <Mono copy>{t.arn}</Mono> },
          { id: "created_at", header: "Created", sortable: true, cell: (t) => formatDate(t.created_at) },
        ]}
      />
      <CreateTopicDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length === 1 ? `topic ${toDelete[0]?.name}` : `${toDelete.length} topics`}?`}
        description="Subscriptions are removed with the topic."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const t of toDelete) await del.mutateAsync(t.name);
        }}
      />
    </div>
  );
}

function CreateTopicDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const router = useRouter();
  const create = useCreateTopic();
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [apiErr, setApiErr] = useState<string | null>(null);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    const v = TOPIC_NAME_RE.test(name) ? null : "Topic names are 1–256 characters: letters, digits, '-' and '_'.";
    setErr(v);
    if (v) return;
    try {
      await create.mutateAsync(name);
      onOpenChange(false);
      setName("");
      router.push(`/sns/${encodeURIComponent(name)}`);
    } catch (e2) {
      setApiErr(errorMessage(e2));
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create topic</DialogTitle>
            <DialogDescription>Add subscriptions after the topic is created.</DialogDescription>
          </DialogHeader>
          <FormError message={apiErr} />
          <FormField label="Topic name" required error={err}>
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create topic
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function TopicDetail({ name }: { name: string }) {
  const router = useRouter();
  const q = useTopic(name);
  const del = useDeleteTopic();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [subOpen, setSubOpen] = useState(false);
  const crumbs = [{ label: "SNS", href: "/sns" }, { label: name }];

  if (q.isLoading) return <LoadingState />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  if (!q.data)
    return (
      <div>
        <PageHeader title={name} breadcrumbs={crumbs} />
        <ErrorState error={new Error(`Topic ${name} was not found.`)} />
      </div>
    );
  const t = q.data;
  return (
    <div className="grid gap-4">
      <PageHeader
        title={t.name}
        breadcrumbs={crumbs}
        actions={
          <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete topic
          </Button>
        }
      />
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "ARN", value: <Mono copy>{t.arn}</Mono> },
            { label: "Subscriptions", value: t.subscriptions?.length ?? "—" },
            { label: "Created", value: formatDate(t.created_at) },
          ]}
        />
      </Section>
      <Section
        title="Subscriptions"
        actions={
          <Button size="sm" variant="outline" onClick={() => setSubOpen(true)}>
            <Plus /> Create subscription
          </Button>
        }
      >
        <ResourceTable
          label="Subscriptions"
          data={t.subscriptions ?? []}
          getRowId={(s) => s.subscription_id ?? s.id ?? `${s.protocol}:${s.endpoint}`}
          globalSearch={false}
          hideSearch
          emptyTitle="No subscriptions"
          emptyDescription="Published messages are not delivered anywhere until you subscribe a queue or log group."
          columns={[
            { id: "protocol", header: "Protocol", sortable: true, cell: (s) => <span className="font-mono text-xs uppercase">{s.protocol}</span> },
            { id: "endpoint", header: "Endpoint", sortable: true, cell: (s) => <Mono copy>{s.endpoint}</Mono> },
            { id: "id", header: "Subscription ID", cell: (s) => <Mono>{s.subscription_id ?? s.id}</Mono> },
          ]}
        />
      </Section>
      <PublishForm topic={t.name} />
      {subOpen && <SubscribeDialog topic={t.name} onClose={() => setSubOpen(false)} />}
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete topic ${t.name}?`}
        confirmLabel="Delete topic"
        destructive
        onConfirm={async () => {
          await del.mutateAsync(t.name);
          router.push("/sns");
        }}
      />
    </div>
  );
}

function SubscribeDialog({ topic, onClose }: { topic: string; onClose: () => void }) {
  const subscribe = useSubscribe();
  const [protocol, setProtocol] = useState<"sqs" | "log">("sqs");
  const [endpoint, setEndpoint] = useState("");
  const [apiErr, setApiErr] = useState<string | null>(null);
  const queues = useAllQueues(protocol === "sqs");
  const logGroups = useLogGroupOptions(protocol === "log");
  const opts =
    protocol === "sqs"
      ? (queues.data ?? []).map((q) => ({ value: q.arn ?? q.name, label: q.name }))
      : (logGroups.data ?? []).map((g) => ({ value: g.name, label: g.name }));
  const loading = protocol === "sqs" ? queues.isLoading : logGroups.isLoading;
  const loadErr = protocol === "sqs" ? queues.error : logGroups.error;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    if (!endpoint) return setApiErr("Choose an endpoint.");
    try {
      await subscribe.mutateAsync({ topic, protocol, endpoint });
      onClose();
    } catch (err) {
      setApiErr(errorMessage(err));
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create subscription</DialogTitle>
            <DialogDescription>Messages published to {topic} are delivered to this endpoint.</DialogDescription>
          </DialogHeader>
          <FormError message={apiErr ?? (loadErr ? errorMessage(loadErr) : null)} />
          <FormField label="Protocol" required>
            {(p) => (
              <NativeSelect
                {...p}
                value={protocol}
                onChange={(e) => {
                  setProtocol(e.target.value as "sqs" | "log");
                  setEndpoint("");
                }}
              >
                <option value="sqs">SQS queue</option>
                <option value="log">CloudWatch log group</option>
              </NativeSelect>
            )}
          </FormField>
          <FormField label={protocol === "sqs" ? "Queue" : "Log group"} required>
            {(p) => (
              <NativeSelect {...p} value={endpoint} onChange={(e) => setEndpoint(e.target.value)}>
                <option value="">{loading ? "Loading…" : opts.length ? "Select an endpoint" : "None available — create one first"}</option>
                {opts.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={subscribe.isPending}>
              {subscribe.isPending && <Loader2 className="animate-spin" />} Subscribe
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PublishForm({ topic }: { topic: string }) {
  const publish = usePublish();
  const [message, setMessage] = useState("");
  const [subject, setSubject] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<unknown>(undefined);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!message) return setError("Message is required.");
    try {
      const r = await publish.mutateAsync({ topic, message, subject: subject || undefined });
      setResult(r ?? null);
    } catch (err) {
      setResult(undefined);
      setError(errorMessage(err));
    }
  };
  return (
    <Section title="Publish message">
      <form onSubmit={submit} noValidate className="grid gap-3">
        <FormError message={error} />
        <FormField label="Subject (optional)">{(p) => <Input {...p} value={subject} onChange={(e) => setSubject(e.target.value)} />}</FormField>
        <FormField label="Message" required>
          {(p) => <Textarea {...p} rows={4} className="font-mono text-xs" value={message} onChange={(e) => setMessage(e.target.value)} />}
        </FormField>
        <div>
          <Button type="submit" disabled={publish.isPending}>
            {publish.isPending ? <Loader2 className="animate-spin" /> : <Megaphone />} Publish
          </Button>
        </div>
        {result !== undefined && (
          <div role="status" className="grid gap-1">
            <span className="text-xs text-muted-foreground">API response</span>
            <pre className="overflow-x-auto rounded-md border bg-muted/40 p-2 font-mono text-xs">{result === null ? "(empty response)" : JSON.stringify(result, null, 2)}</pre>
          </div>
        )}
      </form>
    </Section>
  );
}
