"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { resolveWsUrl } from "@/lib/api/ws";
import { getCsrfToken } from "@/lib/api/client";

export type SocketStatus = "connecting" | "open" | "closed" | "error";

interface Options {
  /** Path below /api/v1, e.g. "/ws/events". */
  path: string;
  enabled?: boolean;
  onMessage: (data: unknown) => void;
  onOpen?: () => void;
  /** Reconnect automatically with backoff (default true). */
  reconnect?: boolean;
}

/**
 * Authenticated WebSocket: the cookie authenticates the upgrade, then the first client message must be
 * `{"csrf_token": "..."}` (docs/api.md). Reconnects with exponential backoff.
 */
export function useSocket({ path, enabled = true, onMessage, onOpen, reconnect = true }: Options) {
  const [status, setStatus] = useState<SocketStatus>("closed");
  const wsRef = useRef<WebSocket | null>(null);
  const handlerRef = useRef(onMessage);
  const openRef = useRef(onOpen);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    handlerRef.current = onMessage;
    openRef.current = onOpen;
  }, [onMessage, onOpen]);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    let retry = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const connect = async () => {
      setStatus("connecting");
      let url: string;
      try {
        url = await resolveWsUrl(path);
      } catch {
        setStatus("error");
        return;
      }
      if (cancelled) return;
      const ws = new WebSocket(url);
      wsRef.current = ws;
      ws.onopen = () => {
        retry = 0;
        ws.send(JSON.stringify({ csrf_token: getCsrfToken() ?? "" }));
        setStatus("open");
        openRef.current?.();
      };
      ws.onmessage = (ev) => {
        let data: unknown = ev.data;
        if (typeof ev.data === "string") {
          try {
            data = JSON.parse(ev.data);
          } catch {
            data = ev.data;
          }
        }
        handlerRef.current(data);
      };
      ws.onerror = () => setStatus("error");
      ws.onclose = () => {
        if (wsRef.current === ws) wsRef.current = null;
        if (cancelled) return;
        setStatus("closed");
        if (reconnect) {
          const delay = Math.min(30_000, 1000 * 2 ** retry++);
          timer = setTimeout(connect, delay);
        }
      };
    };
    void connect();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [path, enabled, reconnect, generation]);

  const send = useCallback((msg: unknown) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(typeof msg === "string" ? msg : JSON.stringify(msg));
      return true;
    }
    return false;
  }, []);

  const reconnectNow = useCallback(() => setGeneration((g) => g + 1), []);

  return { status, send, reconnect: reconnectNow };
}
