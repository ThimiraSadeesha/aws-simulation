"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Download, Eraser, Loader2, Plus, Send, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { Mono } from "@/components/console/copy-button";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { FormError, FormField } from "@/components/console/form-field";
import { PageHeader } from "@/components/console/page-header";
import { ResourceTable } from "@/components/console/resource-table";
import { ErrorState, LoadingState } from "@/components/console/states";
import { useListParams } from "@/hooks/use-list-params";
import { errorMessage } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import type { Queue, QueueMessage } from "@/lib/types";
import {
  messagesAvailable,
  useCreateQueue,
  useDeleteMessage,
  useDeleteQueue,
  usePurgeQueue,
  useQueue,
  useQueues,
  useReceiveMessages,
  useSendMessage,
} from "./api";

export const QUEUE_NAME_RE = /^[A-Za-z0-9_-]{1,80}(\.fifo)?$/;

export function QueuesPage() {
  const list = useListParams({ sort: "name" });
  const q = useQueues(list.params);
  const del = useDeleteQueue();
  const [createOpen, setCreateOpen] = useState(false);
  const [toDelete, setToDelete] = useState<Queue[]>([]);
  return (
    <div>
      <PageHeader title="SQS queues" description="Simulated message queues: send, receive with a visibility timeout, delete and purge. SNS topics can fan out to queues." />
      <ResourceTable
        label="SQS queues"
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        isFetching={q.isFetching}
        error={q.error}
        onRetry={() => void q.refetch()}
        getRowId={(x) => x.name}
        selectable
        bulkActions={[{ label: "Delete", icon: <Trash2 />, destructive: true, onClick: setToDelete }]}
        toolbar={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus /> Create queue
          </Button>
        }
        emptyTitle="No queues"
        columns={[
          {
            id: "name",
            header: "Name",
            sortable: true,
            cell: (x) => (
              <Link href={`/sqs/${encodeURIComponent(x.name)}`} className="font-medium text-sky-700 hover:underline dark:text-sky-400">
                {x.name}
              </Link>
            ),
          },
          { id: "messages", header: "Messages available", cell: (x) => messagesAvailable(x) ?? "—" },
          { id: "in_flight", header: "In flight", cell: (x) => x.messages_in_flight ?? "—" },
          { id: "url", header: "URL / ARN", cell: (x) => <Mono copy>{x.url ?? x.arn}</Mono> },
          { id: "created_at", header: "Created", sortable: true, cell: (x) => formatDate(x.created_at) },
        ]}
      />
      <CreateQueueDialog open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={toDelete.length > 0}
        onOpenChange={(o) => !o && setToDelete([])}
        title={`Delete ${toDelete.length === 1 ? `queue ${toDelete[0]?.name}` : `${toDelete.length} queues`}?`}
        description="All messages in the queue are lost. This cannot be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          for (const x of toDelete) await del.mutateAsync(x.name);
        }}
      />
    </div>
  );
}

function CreateQueueDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const router = useRouter();
  const create = useCreateQueue();
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [apiErr, setApiErr] = useState<string | null>(null);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    const v = QUEUE_NAME_RE.test(name) ? null : "Queue names are 1–80 characters: letters, digits, '-' and '_' (optional .fifo suffix).";
    setErr(v);
    if (v) return;
    try {
      await create.mutateAsync(name);
      onOpenChange(false);
      setName("");
      router.push(`/sqs/${encodeURIComponent(name)}`);
    } catch (e2) {
      setApiErr(errorMessage(e2));
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Create queue</DialogTitle>
            <DialogDescription>Queues are created with the simulator&apos;s default settings.</DialogDescription>
          </DialogHeader>
          <FormError message={apiErr} />
          <FormField label="Queue name" required error={err}>
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} autoFocus />}
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="animate-spin" />} Create queue
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function QueueDetail({ name }: { name: string }) {
  const router = useRouter();
  const q = useQueue(name);
  const del = useDeleteQueue();
  const purge = usePurgeQueue();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmPurge, setConfirmPurge] = useState(false);
  const crumbs = [{ label: "SQS", href: "/sqs" }, { label: name }];

  if (q.isLoading) return <LoadingState />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  if (!q.data)
    return (
      <div>
        <PageHeader title={name} breadcrumbs={crumbs} />
        <ErrorState error={new Error(`Queue ${name} was not found.`)} />
      </div>
    );
  const queue = q.data;

  return (
    <div className="grid gap-4">
      <PageHeader
        title={queue.name}
        breadcrumbs={crumbs}
        actions={
          <>
            <Button size="sm" variant="outline" onClick={() => setConfirmPurge(true)}>
              <Eraser /> Purge
            </Button>
            <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
              <Trash2 /> Delete queue
            </Button>
          </>
        }
      />
      <Section title="Details">
        <DetailGrid
          items={[
            { label: "URL", value: <Mono copy>{queue.url}</Mono> },
            { label: "ARN", value: <Mono copy>{queue.arn}</Mono> },
            { label: "Messages available", value: messagesAvailable(queue) ?? "—" },
            { label: "Messages in flight", value: queue.messages_in_flight ?? "—" },
            { label: "Default visibility timeout", value: queue.visibility_timeout !== undefined ? `${queue.visibility_timeout} s` : "—" },
            { label: "Created", value: formatDate(queue.created_at) },
          ]}
        />
      </Section>
      <SendMessageForm queue={queue.name} />
      <ReceivePanel queue={queue.name} />
      <ConfirmDialog
        open={confirmPurge}
        onOpenChange={setConfirmPurge}
        title={`Purge ${queue.name}?`}
        description="All messages in the queue are deleted. This cannot be undone."
        confirmLabel="Purge"
        destructive
        onConfirm={async () => {
          await purge.mutateAsync(queue.name);
          toast.success(`Purged ${queue.name}`);
        }}
      />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete queue ${queue.name}?`}
        confirmLabel="Delete queue"
        destructive
        typeToConfirm={queue.name}
        onConfirm={async () => {
          await del.mutateAsync(queue.name);
          router.push("/sqs");
        }}
      />
    </div>
  );
}

function SendMessageForm({ queue }: { queue: string }) {
  const send = useSendMessage();
  const [body, setBody] = useState("");
  const [delay, setDelay] = useState("0");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [apiErr, setApiErr] = useState<string | null>(null);
  const [lastResult, setLastResult] = useState<unknown>(null);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiErr(null);
    const errs: Record<string, string> = {};
    if (!body) errs.body = "Message body is required.";
    const d = Number(delay);
    if (!Number.isInteger(d) || d < 0 || d > 900) errs.delay = "Delay must be 0–900 seconds.";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    try {
      const r = await send.mutateAsync({ queue, body, delay_seconds: d || undefined });
      setLastResult(r ?? null);
      setBody("");
      const id = (r as { message_id?: string } | undefined)?.message_id;
      toast.success(id ? `Message ${id} sent` : "Message sent");
    } catch (err) {
      setApiErr(errorMessage(err));
    }
  };
  return (
    <Section title="Send message">
      <form onSubmit={submit} noValidate className="grid gap-3">
        <FormError message={apiErr} />
        <FormField label="Message body" required error={errors.body}>
          {(p) => <Textarea {...p} rows={4} className="font-mono text-xs" value={body} onChange={(e) => setBody(e.target.value)} />}
        </FormField>
        <div className="flex flex-wrap items-end gap-3">
          <FormField label="Delivery delay (seconds)" error={errors.delay} className="w-48">
            {(p) => <Input {...p} type="number" min={0} max={900} value={delay} onChange={(e) => setDelay(e.target.value)} />}
          </FormField>
          <Button type="submit" disabled={send.isPending}>
            {send.isPending ? <Loader2 className="animate-spin" /> : <Send />} Send message
          </Button>
        </div>
        {lastResult !== null && (
          <pre className="overflow-x-auto rounded-md border bg-muted/40 p-2 font-mono text-xs" aria-label="Last send response">
            {JSON.stringify(lastResult, null, 2)}
          </pre>
        )}
      </form>
    </Section>
  );
}

function ReceivePanel({ queue }: { queue: string }) {
  const receive = useReceiveMessages();
  const delMsg = useDeleteMessage();
  const [max, setMax] = useState("10");
  const [visibility, setVisibility] = useState("30");
  const [messages, setMessages] = useState<QueueMessage[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const poll = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const m = Number(max);
    const v = Number(visibility);
    if (!Number.isInteger(m) || m < 1 || m > 10) return setError("Max messages must be 1–10.");
    if (!Number.isInteger(v) || v < 0 || v > 43200) return setError("Visibility timeout must be 0–43200 seconds.");
    try {
      setMessages(await receive.mutateAsync({ queue, max: m, visibility_timeout: v }));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <Section title="Receive messages">
      <form onSubmit={poll} noValidate className="mb-3 flex flex-wrap items-end gap-3">
        <FormField label="Max messages" className="w-32">
          {(p) => <Input {...p} type="number" min={1} max={10} value={max} onChange={(e) => setMax(e.target.value)} />}
        </FormField>
        <FormField label="Visibility timeout (s)" className="w-44">
          {(p) => <Input {...p} type="number" min={0} value={visibility} onChange={(e) => setVisibility(e.target.value)} />}
        </FormField>
        <Button type="submit" variant="outline" disabled={receive.isPending}>
          {receive.isPending ? <Loader2 className="animate-spin" /> : <Download />} Poll for messages
        </Button>
      </form>
      <FormError message={error} />
      {messages !== null && (
        <ResourceTable
          label="Received messages"
          data={messages}
          getRowId={(m) => m.receipt_handle || m.message_id}
          globalSearch={false}
          emptyTitle="No messages received"
          emptyDescription="The queue is empty, or all messages are currently in flight (invisible)."
          searchText={(m) => `${m.message_id} ${m.body}`}
          columns={[
            { id: "message_id", header: "Message ID", cell: (m) => <Mono copy>{m.message_id}</Mono> },
            { id: "body", header: "Body", cell: (m) => <pre className="max-w-xl font-mono text-xs break-all whitespace-pre-wrap">{m.body}</pre> },
            { id: "receive_count", header: "Receives", cell: (m) => m.receive_count ?? "—" },
            { id: "sent_at", header: "Sent", cell: (m) => formatDate(m.sent_at) },
            {
              id: "receipt",
              header: "Receipt handle",
              cell: (m) => <span className="block max-w-40 truncate font-mono text-xs" title={m.receipt_handle}>{m.receipt_handle}</span>,
            },
            {
              id: "a",
              header: "",
              className: "text-right",
              cell: (m) => (
                <Button
                  size="xs"
                  variant="ghost"
                  aria-label={`Delete message ${m.message_id}`}
                  disabled={delMsg.isPending}
                  onClick={async () => {
                    await delMsg.mutateAsync({ queue, receipt: m.receipt_handle });
                    setMessages((prev) => (prev ?? []).filter((x) => x.receipt_handle !== m.receipt_handle));
                  }}
                >
                  <Trash2 /> Delete
                </Button>
              ),
            },
          ]}
        />
      )}
    </Section>
  );
}
