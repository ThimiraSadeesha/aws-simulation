import { Cloud, FlaskConical } from "lucide-react";

export default function AuthLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center bg-muted/30 p-4">
      <div className="mb-6 flex items-center gap-2">
        <span className="flex size-8 items-center justify-center rounded-md bg-sky-600 text-white">
          <Cloud className="size-4" aria-hidden />
        </span>
        <span className="text-lg font-semibold tracking-tight">CloudSim Console</span>
      </div>
      <div className="w-full max-w-sm">{children}</div>
      <p className="mt-6 flex items-center gap-1.5 text-xs text-muted-foreground" data-testid="simulated-indicator">
        <FlaskConical className="size-3.5" aria-hidden /> Simulated environment — no real cloud resources
      </p>
    </div>
  );
}
