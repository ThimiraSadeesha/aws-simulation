"use client";

import { useCallback, useRef, useState } from "react";
import { cn } from "@/lib/utils";

/** Two resizable panes (horizontal on large screens, stacked below lg). Keyboard: ←/→ on the divider. */
export function SplitPane({
  left,
  right,
  initial = 45,
  min = 25,
  max = 75,
  label = "Resize panes",
  className,
}: {
  left: React.ReactNode;
  right: React.ReactNode;
  initial?: number;
  min?: number;
  max?: number;
  label?: string;
  className?: string;
}) {
  const [pct, setPct] = useState(initial);
  const ref = useRef<HTMLDivElement>(null);
  const clamp = useCallback((v: number) => Math.min(max, Math.max(min, v)), [min, max]);

  const onPointerDown = (e: React.PointerEvent) => {
    e.preventDefault();
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const move = (ev: PointerEvent) => setPct(clamp(((ev.clientX - rect.left) / rect.width) * 100));
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  return (
    <div ref={ref} className={cn("flex min-h-0 flex-col gap-3 lg:flex-row lg:gap-0", className)} style={{ ["--split" as string]: `${pct}%` }}>
      <div className="min-h-0 min-w-0 lg:w-[var(--split)] lg:pr-2">{left}</div>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={label}
        aria-valuemin={min}
        aria-valuemax={max}
        aria-valuenow={Math.round(pct)}
        tabIndex={0}
        onPointerDown={onPointerDown}
        onKeyDown={(e) => {
          if (e.key === "ArrowLeft") setPct((p) => clamp(p - 5));
          if (e.key === "ArrowRight") setPct((p) => clamp(p + 5));
        }}
        className="hidden w-1.5 shrink-0 cursor-col-resize rounded bg-border transition-colors outline-none hover:bg-sky-500/60 focus-visible:bg-sky-500 lg:block"
      />
      <div className="min-h-0 min-w-0 flex-1 lg:pl-2">{right}</div>
    </div>
  );
}
