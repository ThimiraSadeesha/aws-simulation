"use client";

import { useState } from "react";
import { QueryClient, QueryClientProvider, MutationCache } from "@tanstack/react-query";
import { ThemeProvider } from "next-themes";
import { toast } from "sonner";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AuthProvider } from "@/features/auth/auth-context";
import { ApiError, errorMessage } from "@/lib/api/client";

export function makeQueryClient() {
  return new QueryClient({
    queryCache: undefined,
    mutationCache: new MutationCache({
      onError: (err, _vars, _ctx, mutation) => {
        // Mutations surface `error.message` in a toast unless they opt out (inline form errors).
        if (mutation.meta?.silent) return;
        if (err instanceof ApiError && err.status === 401) return;
        toast.error(errorMessage(err), {
          description: err instanceof ApiError && err.code ? `${err.code}${err.requestId ? ` · ${err.requestId}` : ""}` : undefined,
        });
      },
    }),
    defaultOptions: {
      queries: {
        staleTime: 15_000,
        refetchOnWindowFocus: true,
        retry: (count, err) => {
          if (err instanceof ApiError && err.status >= 400 && err.status < 500) return false;
          return count < 2;
        },
      },
    },
  });
}

export function Providers({ children }: { children: React.ReactNode }) {
  const [client] = useState(makeQueryClient);
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <QueryClientProvider client={client}>
        <TooltipProvider delayDuration={300}>
          <AuthProvider>{children}</AuthProvider>
          <Toaster richColors closeButton position="bottom-right" />
        </TooltipProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
