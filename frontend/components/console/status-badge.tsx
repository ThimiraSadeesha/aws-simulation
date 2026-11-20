import { cn } from "@/lib/utils";

export type Tone = "success" | "warning" | "danger" | "neutral" | "info" | "muted";

const TONES: Record<string, Tone> = {
  running: "success",
  available: "success",
  active: "success",
  attached: "success",
  healthy: "success",
  ok: "success",
  enabled: "success",
  passed: "success",
  completed: "success",
  allowed: "success",
  allow: "success",
  successful: "success",
  "in-use": "info",
  in_progress: "info",
  inprogress: "warning",
  pending: "warning",
  stopping: "warning",
  rebooting: "warning",
  provisioning: "warning",
  "shutting-down": "warning",
  deleting: "warning",
  initial: "warning",
  creating: "warning",
  starting: "warning",
  stopped: "neutral",
  detached: "neutral",
  unused: "neutral",
  suspended: "neutral",
  disabled: "neutral",
  not_started: "neutral",
  insufficient_data: "neutral",
  inactive: "neutral",
  terminated: "muted",
  deleted: "muted",
  alarm: "danger",
  unhealthy: "danger",
  failed: "danger",
  blackhole: "danger",
  denied: "danger",
  deny: "danger",
  error: "danger",
  explicitdeny: "danger",
  implicitdeny: "danger",
  unavailable: "danger",
  degraded: "warning",
};

export const TRANSITIONAL_STATES = new Set([
  "pending",
  "stopping",
  "rebooting",
  "shutting-down",
  "provisioning",
  "deleting",
  "creating",
  "starting",
  "initial",
  "InProgress",
]);

export function toneFor(status: string | undefined | null): Tone {
  if (!status) return "neutral";
  return TONES[status.toLowerCase()] ?? "neutral";
}

const toneClass: Record<Tone, string> = {
  success: "bg-emerald-500/10 text-emerald-700 ring-emerald-600/25 dark:text-emerald-400",
  warning: "bg-amber-500/10 text-amber-700 ring-amber-600/25 dark:text-amber-400",
  danger: "bg-red-500/10 text-red-700 ring-red-600/25 dark:text-red-400",
  info: "bg-sky-500/10 text-sky-700 ring-sky-600/25 dark:text-sky-400",
  neutral: "bg-muted text-muted-foreground ring-border",
  muted: "bg-transparent text-muted-foreground/80 ring-border line-through decoration-1",
};

const dotClass: Record<Tone, string> = {
  success: "bg-emerald-500",
  warning: "bg-amber-500 animate-pulse",
  danger: "bg-red-500",
  info: "bg-sky-500",
  neutral: "bg-muted-foreground/60",
  muted: "bg-muted-foreground/40",
};

export function StatusBadge({ status, label, className }: { status: string | undefined | null; label?: string; className?: string }) {
  const tone = toneFor(status);
  const text = label ?? (status ? status.replace(/_/g, " ") : "unknown");
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap ring-1 ring-inset",
        toneClass[tone],
        className,
      )}
      data-tone={tone}
    >
      <span className={cn("size-1.5 rounded-full", dotClass[tone])} aria-hidden />
      {text}
    </span>
  );
}
