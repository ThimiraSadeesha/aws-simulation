"use client";

import Link from "next/link";
import { Clock, GraduationCap } from "lucide-react";
import { PageHeader } from "@/components/console/page-header";
import { StatusBadge } from "@/components/console/status-badge";
import { EmptyState, ErrorState, LoadingState } from "@/components/console/states";
import { Progress } from "@/components/ui/progress";
import { useLabs } from "@/features/labs/api";
import { useGlobalSearch } from "@/hooks/use-global-search";
import type { LabLevel, LabSummary } from "@/lib/types";

const LEVELS: { level: LabLevel; title: string; description: string }[] = [
  { level: "beginner", title: "Beginner", description: "First steps: launch an instance, create a bucket, explore the console." },
  { level: "intermediate", title: "Intermediate", description: "Networking, IAM policies, load balancing and monitoring." },
  { level: "advanced", title: "Advanced", description: "Multi-tier architectures, auto scaling and troubleshooting." },
];

function LabCard({ lab }: { lab: LabSummary }) {
  const pct = lab.total_steps ? Math.round((lab.completed_steps / lab.total_steps) * 100) : 0;
  return (
    <Link
      href={`/labs/${encodeURIComponent(lab.id)}`}
      className="group flex flex-col gap-2 rounded-lg border bg-card p-4 transition-colors outline-none hover:border-sky-500/50 focus-visible:ring-2 focus-visible:ring-ring/60"
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-semibold group-hover:underline">{lab.title}</h3>
        <StatusBadge status={lab.status} />
      </div>
      <p className="line-clamp-3 text-xs text-muted-foreground">{lab.description}</p>
      <div className="mt-auto flex items-center gap-3 text-xs text-muted-foreground">
        <span className="rounded bg-muted px-1.5 py-0.5 font-medium uppercase">{lab.service}</span>
        <span className="flex items-center gap-1">
          <Clock className="size-3" aria-hidden /> {lab.duration_min} min
        </span>
        <span className="ml-auto">
          {lab.completed_steps}/{lab.total_steps} steps
        </span>
      </div>
      <Progress value={pct} aria-label={`${lab.title} progress`} className="h-1.5" />
    </Link>
  );
}

export default function LabsPage() {
  const labs = useLabs();
  const { query } = useGlobalSearch();
  const q = query.trim().toLowerCase();
  const items = (labs.data ?? []).filter(
    (l) => !q || `${l.title} ${l.service} ${l.description} ${l.id}`.toLowerCase().includes(q),
  );
  const done = (labs.data ?? []).filter((l) => l.status === "completed").length;

  return (
    <div>
      <PageHeader
        title="Training Labs"
        description="Guided, hands-on exercises. Each step is validated against the real state of your simulated account."
        actions={
          labs.data && (
            <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
              <GraduationCap className="size-4" aria-hidden /> {done} of {labs.data.length} labs completed
            </span>
          )
        }
      />
      {labs.isLoading ? (
        <LoadingState label="Loading labs…" />
      ) : labs.isError ? (
        <ErrorState error={labs.error} onRetry={() => void labs.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title={q ? "No labs match your search" : "No labs available"} description={q ? undefined : "The backend did not return any training labs."} />
      ) : (
        <div className="grid gap-6">
          {LEVELS.map(({ level, title, description }) => {
            const group = items.filter((l) => l.level === level);
            if (group.length === 0) return null;
            return (
              <section key={level} aria-labelledby={`level-${level}`}>
                <h2 id={`level-${level}`} className="text-base font-semibold">
                  {title} <span className="text-sm font-normal text-muted-foreground">({group.length})</span>
                </h2>
                <p className="mb-3 text-xs text-muted-foreground">{description}</p>
                <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                  {group.map((l) => (
                    <LabCard key={l.id} lab={l} />
                  ))}
                </div>
              </section>
            );
          })}
        </div>
      )}
    </div>
  );
}
