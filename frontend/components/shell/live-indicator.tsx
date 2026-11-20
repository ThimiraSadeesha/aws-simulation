"use client";

import { Radio } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { useLiveEvents } from "@/features/events/events-provider";
import { formatTime } from "@/lib/format";
import { cn } from "@/lib/utils";

const STATUS_TEXT = {
  open: "Live",
  connecting: "Connecting",
  closed: "Offline",
  error: "Offline",
} as const;

export function LiveIndicator() {
  const { status, events, unread, markRead } = useLiveEvents();
  return (
    <Popover onOpenChange={(o) => o && markRead()}>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="sm" aria-label={`Event stream: ${STATUS_TEXT[status]}${unread ? `, ${unread} new events` : ""}`} className="gap-1.5">
          <span className="relative flex size-2">
            {status === "open" && <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-400 opacity-60" />}
            <span
              className={cn(
                "relative inline-flex size-2 rounded-full",
                status === "open" ? "bg-emerald-500" : status === "connecting" ? "bg-amber-500" : "bg-muted-foreground/50",
              )}
            />
          </span>
          <span className="hidden text-xs sm:inline">{STATUS_TEXT[status]}</span>
          {unread > 0 && <span className="rounded-full bg-sky-600 px-1.5 text-[10px] font-semibold text-white">{unread > 99 ? "99+" : unread}</span>}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96 p-0">
        <div className="flex items-center gap-2 border-b px-3 py-2 text-sm font-medium">
          <Radio className="size-4" aria-hidden /> Live events
          <span className="ml-auto text-xs font-normal text-muted-foreground">/ws/events · {STATUS_TEXT[status]}</span>
        </div>
        <ul className="max-h-80 overflow-y-auto text-xs" aria-label="Recent live events">
          {events.length === 0 ? (
            <li className="px-3 py-6 text-center text-muted-foreground">No events received in this session yet.</li>
          ) : (
            events.map((e, i) => (
              <li key={`${e.id}-${i}`} className="border-b px-3 py-2 last:border-b-0">
                <div className="flex items-center gap-2">
                  <span className="font-mono">{e.resource_id}</span>
                  <span className="text-muted-foreground">{e.type}</span>
                  <span className="ml-auto text-muted-foreground">{formatTime(e.ts)}</span>
                </div>
                <div className="mt-0.5 text-muted-foreground">{e.message}</div>
              </li>
            ))
          )}
        </ul>
      </PopoverContent>
    </Popover>
  );
}
