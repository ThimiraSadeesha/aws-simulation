"use client";

import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { loginRedirectPath, useAuth } from "./auth-context";

/** Guards the console: unauthenticated users are redirected to /login. */
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { status, error, refresh } = useAuth();
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (status === "unauthenticated") router.replace(loginRedirectPath(pathname));
  }, [status, router, pathname]);

  if (status === "authenticated") return <>{children}</>;
  if (status === "error") {
    return (
      <div role="alert" className="flex min-h-screen flex-col items-center justify-center gap-3 p-6 text-center">
        <p className="text-sm font-medium">Could not contact the CloudSim backend.</p>
        <p className="max-w-md text-sm text-muted-foreground">{error}</p>
        <Button size="sm" variant="outline" onClick={() => void refresh()}>
          Retry
        </Button>
      </div>
    );
  }
  return (
    <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground" aria-live="polite">
      <Loader2 className="mr-2 size-4 animate-spin" aria-hidden /> Loading console…
    </div>
  );
}
