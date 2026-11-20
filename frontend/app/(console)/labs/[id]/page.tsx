"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { CheckCircle2, Circle, ExternalLink, Lightbulb, Loader2, Play, RotateCcw, ShieldCheck, XCircle } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ConfirmDialog } from "@/components/console/confirm-dialog";
import { PageHeader } from "@/components/console/page-header";
import { SplitPane } from "@/components/console/split-pane";
import { StatusBadge } from "@/components/console/status-badge";
import { ErrorState, LoadingState } from "@/components/console/states";
import { consolePathForService, passedSteps, useLab, useResetLab, useStartLab, useValidateStep } from "@/features/labs/api";
import { LabTerminal } from "@/features/labs/terminal";
import { formatDate } from "@/lib/format";
import type { Lab, LabStepProgress, ValidateStepResponse } from "@/lib/types";
import { cn } from "@/lib/utils";

function StepIcon({ status }: { status: LabStepProgress["status"] | undefined }) {
  if (status === "passed") return <CheckCircle2 className="size-4 text-emerald-500" aria-label="passed" />;
  if (status === "failed") return <XCircle className="size-4 text-red-500" aria-label="failed" />;
  return <Circle className="size-4 text-muted-foreground" aria-label="pending" />;
}

function LabInstructions({ lab }: { lab: Lab }) {
  const firstOpen = lab.progress?.steps?.find((s) => s.status !== "passed")?.index ?? 0;
  const [selected, setSelected] = useState<number>(firstOpen);
  const [showHint, setShowHint] = useState(false);
  const [result, setResult] = useState<Record<number, ValidateStepResponse>>({});
  const validate = useValidateStep(lab.id);
  const started = !!lab.progress && lab.progress.status !== "not_started";
  const steps = [...lab.steps].sort((a, b) => a.index - b.index);
  const step = steps.find((s) => s.index === selected) ?? steps[0];
  const stepProgress = (i: number) => lab.progress?.steps?.find((s) => s.index === i);

  const runValidate = async () => {
    if (!step) return;
    try {
      const r = await validate.mutateAsync(step.index);
      setResult((m) => ({ ...m, [step.index]: r }));
      if (r.passed) toast.success(`Step ${step.index + 1} passed`, { description: r.message });
    } catch {
      // error toast is shown by the mutation cache
    }
  };

  if (!step) return <p className="text-sm text-muted-foreground">This lab has no steps.</p>;
  const res = result[step.index];
  const prog = stepProgress(step.index);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <ol className="grid gap-1" aria-label="Lab steps">
        {steps.map((s) => {
          const p = stepProgress(s.index);
          return (
            <li key={s.index}>
              <button
                type="button"
                onClick={() => {
                  setSelected(s.index);
                  setShowHint(false);
                }}
                aria-current={s.index === step.index ? "step" : undefined}
                className={cn(
                  "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
                  s.index === step.index ? "bg-primary/10 font-medium" : "hover:bg-muted",
                )}
              >
                <StepIcon status={p?.status} />
                <span className="text-muted-foreground">{s.index + 1}.</span>
                <span className="truncate">{s.title}</span>
              </button>
            </li>
          );
        })}
      </ol>

      <article className="min-h-0 flex-1 overflow-y-auto rounded-lg border bg-card p-4" aria-labelledby="step-title">
        <h2 id="step-title" className="text-base font-semibold">
          Step {step.index + 1}: {step.title}
        </h2>
        <div className="prose-console mt-2">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{step.instructions_md}</ReactMarkdown>
        </div>
        {step.hint && (
          <div className="mt-3">
            <Button size="sm" variant="ghost" aria-expanded={showHint} onClick={() => setShowHint((v) => !v)}>
              <Lightbulb /> {showHint ? "Hide hint" : "Show hint"}
            </Button>
            {showHint && (
              <div className="prose-console mt-1 rounded-md border border-amber-500/30 bg-amber-500/5 p-3">
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{step.hint}</ReactMarkdown>
              </div>
            )}
          </div>
        )}
        <div className="mt-4 flex flex-wrap items-center gap-2 border-t pt-3">
          <Button size="sm" onClick={() => void runValidate()} disabled={!started || validate.isPending}>
            {validate.isPending ? <Loader2 className="animate-spin" /> : <ShieldCheck />} Validate step
          </Button>
          {!started && <span className="text-xs text-muted-foreground">Start the lab to validate steps.</span>}
          {prog?.checked_at && !res && (
            <span className="text-xs text-muted-foreground">Last checked {formatDate(prog.checked_at)}</span>
          )}
        </div>
        {(res || (prog && prog.status !== "pending" && prog.message)) && (
          <div
            role="status"
            data-testid="step-result"
            className={cn(
              "mt-3 flex items-start gap-2 rounded-md border px-3 py-2 text-sm",
              (res ? res.passed : prog?.status === "passed")
                ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-300"
                : "border-red-500/30 bg-red-500/10 text-red-800 dark:text-red-300",
            )}
          >
            {(res ? res.passed : prog?.status === "passed") ? (
              <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden />
            ) : (
              <XCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
            )}
            <div>
              <div className="font-medium">{(res ? res.passed : prog?.status === "passed") ? "Passed" : "Not yet"}</div>
              <div>{res ? res.message : prog?.message}</div>
            </div>
          </div>
        )}
      </article>
    </div>
  );
}

export default function LabRunnerPage() {
  const { id } = useParams<{ id: string }>();
  const labId = decodeURIComponent(id ?? "");
  const lab = useLab(labId);
  const start = useStartLab(labId);
  const reset = useResetLab(labId);
  const [confirmReset, setConfirmReset] = useState(false);

  if (lab.isLoading) return <LoadingState label="Loading lab…" />;
  if (lab.isError || !lab.data) return <ErrorState error={lab.error} onRetry={() => void lab.refetch()} />;

  const l = lab.data;
  const total = l.steps.length;
  const passed = passedSteps(l);
  const pct = total ? Math.round((passed / total) * 100) : 0;
  const status = l.progress?.status ?? "not_started";
  const consolePath = consolePathForService(l.service);

  return (
    <div className="flex flex-col">
      <PageHeader
        breadcrumbs={[{ label: "Training Labs", href: "/labs" }, { label: l.title }]}
        title={l.title}
        description={l.description}
        actions={
          <>
            <StatusBadge status={status} />
            {status === "not_started" ? (
              <Button size="sm" onClick={() => start.mutate(undefined, { onSuccess: () => toast.success("Lab started") })} disabled={start.isPending}>
                {start.isPending ? <Loader2 className="animate-spin" /> : <Play />} Start lab
              </Button>
            ) : null}
            <Button size="sm" variant="outline" onClick={() => setConfirmReset(true)}>
              <RotateCcw /> Reset lab
            </Button>
          </>
        }
      />
      <div className="mb-3 flex items-center gap-3">
        <Progress value={pct} className="h-2 flex-1" aria-label="Lab progress" />
        <span className="text-xs whitespace-nowrap text-muted-foreground" data-testid="lab-progress-text">
          {passed}/{total} steps · {pct}%
        </span>
      </div>
      <SplitPane
        className="h-[calc(100vh-14rem)] min-h-[520px]"
        left={<LabInstructions key={`${l.id}-${status}`} lab={l} />}
        right={
          <Tabs defaultValue="terminal" className="flex h-full min-h-[420px] flex-col">
            <TabsList>
              <TabsTrigger value="terminal">Terminal</TabsTrigger>
              <TabsTrigger value="console">Console</TabsTrigger>
            </TabsList>
            <TabsContent value="terminal" forceMount className="min-h-0 flex-1 data-[state=inactive]:hidden">
              <LabTerminal className="h-full" />
            </TabsContent>
            <TabsContent value="console" className="flex min-h-0 flex-1 flex-col gap-1">
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>Embedded console: {consolePath}</span>
                <a href={consolePath} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:text-foreground">
                  Open in new tab <ExternalLink className="size-3" aria-hidden />
                </a>
              </div>
              <iframe title="CloudSim console" src={consolePath} className="min-h-0 w-full flex-1 rounded-lg border bg-background" />
            </TabsContent>
          </Tabs>
        }
      />
      <ConfirmDialog
        open={confirmReset}
        onOpenChange={setConfirmReset}
        title="Reset lab?"
        destructive
        confirmLabel="Reset lab"
        description="This deletes all resources tagged cloudsim:lab or named with this lab's prefix, and resets your progress."
        onConfirm={() => reset.mutateAsync().then(() => toast.success("Lab reset"))}
      />
    </div>
  );
}
