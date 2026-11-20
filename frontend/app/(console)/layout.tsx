"use client";

import { useSyncExternalStore } from "react";
import { RequireAuth } from "@/features/auth/require-auth";
import { EventsProvider } from "@/features/events/events-provider";
import { GlobalSearchProvider } from "@/hooks/use-global-search";
import { Sidebar } from "@/components/shell/sidebar";
import { Topbar } from "@/components/shell/topbar";

const noopSubscribe = () => () => {};
/** True when rendered inside an iframe (the lab runner embeds console pages next to the lab). */
function useEmbedded() {
  return useSyncExternalStore(
    noopSubscribe,
    () => window.self !== window.top,
    () => false,
  );
}

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const embedded = useEmbedded();
  if (embedded) {
    return (
      <RequireAuth>
        <GlobalSearchProvider>
          <EventsProvider enabled={false}>
            <main id="main" className="min-w-0 p-3">
              {children}
            </main>
          </EventsProvider>
        </GlobalSearchProvider>
      </RequireAuth>
    );
  }
  return (
    <RequireAuth>
      <GlobalSearchProvider>
        <EventsProvider>
          <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:bg-background focus:px-3 focus:py-2">
            Skip to content
          </a>
          <div className="flex min-h-screen">
            <Sidebar />
            <div className="flex min-w-0 flex-1 flex-col">
              <Topbar />
              <main id="main" className="min-w-0 flex-1 p-4 md:p-6">
                {children}
              </main>
            </div>
          </div>
        </EventsProvider>
      </GlobalSearchProvider>
    </RequireAuth>
  );
}
