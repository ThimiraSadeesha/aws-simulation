import { Construction } from "lucide-react";
import { PageHeader } from "./page-header";

/** Honest placeholder for services the API marks as not implemented (501 NotImplemented). */
export function NotAvailable({ service, summary, planned }: { service: string; summary: string; planned: string[] }) {
  return (
    <div>
      <PageHeader title={service} description={summary} />
      <div className="rounded-lg border bg-card p-6">
        <div className="flex items-start gap-3">
          <Construction className="mt-0.5 size-5 text-amber-500" aria-hidden />
          <div className="grid gap-2">
            <h2 className="text-base font-semibold">Not yet available in this simulator release</h2>
            <p className="max-w-2xl text-sm text-muted-foreground">
              The CloudSim backend does not implement {service} yet — its API endpoints return <code className="font-mono">501 NotImplemented</code>.
              Rather than show a mock interface, this page explains what is planned.
            </p>
            <h3 className="mt-2 text-sm font-medium">Planned scope</h3>
            <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
              {planned.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </div>
        </div>
      </div>
    </div>
  );
}
