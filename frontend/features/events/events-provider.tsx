"use client";

import { createContext, useCallback, useContext, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useSocket, type SocketStatus } from "@/hooks/use-socket";
import type { CloudEvent, EventsWsMessage, MetricSample } from "@/lib/types";

interface EventsContextValue {
  status: SocketStatus;
  events: CloudEvent[];
  samples: MetricSample[];
  lastEventAt: number | null;
  unread: number;
  markRead: () => void;
}

const EventsContext = createContext<EventsContextValue>({
  status: "closed",
  events: [],
  samples: [],
  lastEventAt: null,
  unread: 0,
  markRead: () => {},
});

/** Which query-key roots to refresh when an event for a resource type arrives. */
export const RESOURCE_TYPE_QUERY_ROOTS: Record<string, string[]> = {
  instance: ["ec2", "vpc", "elb", "autoscaling"],
  nat_gateway: ["vpc"],
  load_balancer: ["elb"],
  target_group: ["elb"],
  autoscaling_group: ["autoscaling", "ec2"],
  bucket: ["s3"],
  alarm: ["cloudwatch"],
  vpc: ["vpc"],
  subnet: ["vpc"],
};

/**
 * Subscribes to /api/v1/ws/events. Pushes events into a small in-memory feed for the live indicator
 * and invalidates the affected TanStack Query caches so lists refresh without polling.
 */
export function EventsProvider({ children, enabled = true }: { children: React.ReactNode; enabled?: boolean }) {
  const qc = useQueryClient();
  const [events, setEvents] = useState<CloudEvent[]>([]);
  const [samples, setSamples] = useState<MetricSample[]>([]);
  const [lastEventAt, setLastEventAt] = useState<number | null>(null);
  const [unread, setUnread] = useState(0);
  const pending = useRef<Set<string>>(new Set());
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const scheduleInvalidate = useCallback(
    (roots: string[]) => {
      roots.forEach((r) => pending.current.add(r));
      if (timer.current) return;
      timer.current = setTimeout(() => {
        const keys = [...pending.current];
        pending.current.clear();
        timer.current = null;
        for (const k of keys) void qc.invalidateQueries({ queryKey: [k] });
        void qc.invalidateQueries({ queryKey: ["dashboard"] });
        void qc.invalidateQueries({ queryKey: ["events"] });
      }, 500);
    },
    [qc],
  );

  const onMessage = useCallback(
    (raw: unknown) => {
      const msg = raw as EventsWsMessage;
      if (!msg || typeof msg !== "object") return;
      if (msg.type === "event" && msg.event) {
        setEvents((prev) => [msg.event, ...prev].slice(0, 50));
        setLastEventAt(Date.now());
        setUnread((n) => n + 1);
        scheduleInvalidate(RESOURCE_TYPE_QUERY_ROOTS[msg.event.resource_type] ?? [msg.event.resource_type]);
      } else if (msg.type === "metrics" && Array.isArray(msg.samples)) {
        setSamples(msg.samples);
      }
    },
    [scheduleInvalidate],
  );

  const { status } = useSocket({ path: "/ws/events", enabled, onMessage });
  const markRead = useCallback(() => setUnread(0), []);

  return (
    <EventsContext.Provider value={{ status, events, samples, lastEventAt, unread, markRead }}>{children}</EventsContext.Provider>
  );
}

export function useLiveEvents() {
  return useContext(EventsContext);
}
