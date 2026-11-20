"use client";

import { toast } from "sonner";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import type { InstanceState } from "@/lib/types";
import { useInstanceAction, type InstanceAction } from "./api";

/** Valid source states per action (mirrors the engine's state machine, docs/engine-protocol.md §2). */
export const ACTION_ALLOWED: Record<InstanceAction, InstanceState[]> = {
  start: ["stopped"],
  stop: ["running"],
  reboot: ["running"],
  terminate: ["pending", "running", "stopping", "stopped", "rebooting"],
};

export const ACTION_LABEL: Record<InstanceAction, string> = {
  start: "Start",
  stop: "Stop",
  reboot: "Reboot",
  terminate: "Terminate",
};

export function canDo(action: InstanceAction, state: InstanceState | string) {
  return (ACTION_ALLOWED[action] as string[]).includes(state);
}

const DESCRIPTION: Record<InstanceAction, string> = {
  start: "The instance(s) will move to pending and then running.",
  stop: "The instance(s) will move to stopping and then stopped. Public IPs may change on next start.",
  reboot: "The instance(s) will reboot (rebooting → running).",
  terminate: "Terminated instances cannot be restarted. Their network interfaces are released.",
};

export function InstanceActionDialog({
  action,
  ids,
  onClose,
}: {
  action: InstanceAction | null;
  ids: string[];
  onClose: () => void;
}) {
  const mutation = useInstanceAction();
  if (!action) return null;
  const label = ACTION_LABEL[action];
  return (
    <ConfirmDialog
      open={!!action}
      onOpenChange={(o) => !o && onClose()}
      title={`${label} ${ids.length === 1 ? "instance" : `${ids.length} instances`}?`}
      description={
        <div className="grid gap-2">
          <p>{DESCRIPTION[action]}</p>
          <ul className="font-mono text-xs">
            {ids.map((id) => (
              <li key={id}>{id}</li>
            ))}
          </ul>
        </div>
      }
      confirmLabel={label}
      destructive={action === "terminate" || action === "stop"}
      onConfirm={async () => {
        const results = await Promise.allSettled(ids.map((id) => mutation.mutateAsync({ id, action })));
        const ok = results.filter((r) => r.status === "fulfilled").length;
        if (ok > 0) toast.success(`${label} requested for ${ok} instance${ok === 1 ? "" : "s"}`);
        if (ok === 0) throw new Error("all failed");
      }}
    />
  );
}
