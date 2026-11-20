"use client";

import { useEffect, useRef, useState } from "react";
import "@xterm/xterm/css/xterm.css";
import type { Terminal as XTerm } from "@xterm/xterm";
import type { FitAddon } from "@xterm/addon-fit";
import { RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useSocket } from "@/hooks/use-socket";
import { cn } from "@/lib/utils";

interface TermMessage {
  type?: string;
  data?: string;
}

const STATUS_LABEL = { open: "Connected", connecting: "Connecting…", closed: "Disconnected", error: "Connection error" } as const;

/**
 * Lab terminal: xterm.js over WebSocket /api/v1/ws/terminal.
 * Protocol (docs/api.md): first message `{"csrf_token"}`, then `{"type":"input","data":<keystrokes>}`;
 * the server replies `{"type":"output","data"}`. Commands are run by the backend's built-in `cloudsim`
 * interpreter — no shell is spawned. Keystroke echo/line editing is done by the server.
 */
export function LabTerminal({ className }: { className?: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const pendingOutput = useRef<string[]>([]);
  const [ready, setReady] = useState(false);

  const { status, send, reconnect } = useSocket({
    path: "/ws/terminal",
    enabled: ready,
    onMessage: (raw) => {
      const msg = raw as TermMessage | string;
      const text = typeof msg === "string" ? msg : msg?.type === "output" || msg?.type === undefined ? (msg?.data ?? "") : msg.type === "error" ? `\r\n\x1b[31m${msg.data ?? "error"}\x1b[0m\r\n` : "";
      if (!text) return;
      if (termRef.current) termRef.current.write(text);
      else pendingOutput.current.push(text);
    },
    onOpen: () => termRef.current?.writeln("\x1b[2m# connected to the CloudSim terminal (simulated – no real shell)\x1b[0m"),
  });

  const sendRef = useRef(send);
  useEffect(() => {
    sendRef.current = send;
  }, [send]);

  // Create the xterm instance once (dynamic import: xterm needs `window`).
  useEffect(() => {
    let disposed = false;
    let cleanup: (() => void) | undefined;
    void (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]);
      if (disposed || !containerRef.current) return;
      const term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
        theme: { background: "#0b1020", foreground: "#e5e7eb", cursor: "#38bdf8" },
        convertEol: false,
        scrollback: 5000,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(containerRef.current);
      try {
        fit.fit();
      } catch {
        // container not measurable yet
      }
      termRef.current = term;
      fitRef.current = fit;
      for (const chunk of pendingOutput.current) term.write(chunk);
      pendingOutput.current = [];
      const sub = term.onData((data) => {
        sendRef.current({ type: "input", data });
      });
      const ro = new ResizeObserver(() => {
        try {
          fit.fit();
        } catch {
          // ignore
        }
      });
      ro.observe(containerRef.current);
      cleanup = () => {
        ro.disconnect();
        sub.dispose();
        term.dispose();
        termRef.current = null;
      };
      setReady(true);
    })();
    return () => {
      disposed = true;
      cleanup?.();
    };
  }, []);

  return (
    <div className={cn("flex min-h-0 flex-col overflow-hidden rounded-lg border bg-[#0b1020]", className)}>
      <div className="flex items-center gap-2 border-b border-white/10 px-3 py-1.5 text-xs text-slate-300">
        <span className={cn("size-2 rounded-full", status === "open" ? "bg-emerald-400" : status === "connecting" ? "bg-amber-400" : "bg-slate-500")} aria-hidden />
        <span role="status">Terminal · {STATUS_LABEL[status]}</span>
        <span className="hidden text-slate-500 sm:inline">Try: cloudsim ec2 describe-instances · help</span>
        <Button size="xs" variant="ghost" className="ml-auto text-slate-300 hover:bg-white/10 hover:text-white" onClick={reconnect} aria-label="Reconnect terminal">
          <RotateCw /> Reconnect
        </Button>
      </div>
      <div ref={containerRef} className="min-h-0 flex-1 p-2" role="application" aria-label="Lab terminal" data-testid="lab-terminal" />
    </div>
  );
}
