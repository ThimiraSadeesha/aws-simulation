"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";

export function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      size="icon-xs"
      variant="ghost"
      aria-label={copied ? "Copied" : `${label}: ${value.length > 40 ? value.slice(0, 40) + "…" : value}`}
      onClick={async (e) => {
        e.stopPropagation();
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          // clipboard unavailable (insecure context) – ignore
        }
      }}
    >
      {copied ? <Check /> : <Copy />}
    </Button>
  );
}

export function Mono({ children, copy }: { children: string | undefined | null; copy?: boolean }) {
  if (!children) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="inline-flex items-center gap-0.5 font-mono text-xs">
      {children}
      {copy && <CopyButton value={children} />}
    </span>
  );
}
