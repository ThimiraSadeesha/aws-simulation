"use client";

import { Download, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { downloadText } from "@/lib/format";
import { CopyButton } from "./copy-button";

/** Displays secret material exactly once (key pair private key, access key secret). */
export function SecretOnceDialog({
  open,
  onClose,
  title,
  fields,
  download,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  fields: { label: string; value: string; multiline?: boolean }[];
  download?: { filename: string; content: string };
}) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription asChild>
            <div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-amber-800 dark:text-amber-300">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden />
              <span>This is the only time the secret is shown. CloudSim does not store it in a retrievable form — save it now.</span>
            </div>
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          {fields.map((f) => (
            <div key={f.label} className="grid gap-1">
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>{f.label}</span>
                <CopyButton value={f.value} label={`Copy ${f.label}`} />
              </div>
              {f.multiline ? (
                <pre className="max-h-64 overflow-auto rounded-md border bg-muted/50 p-2 font-mono text-xs" data-testid={`secret-${f.label}`}>
                  {f.value}
                </pre>
              ) : (
                <code className="rounded-md border bg-muted/50 px-2 py-1 font-mono text-xs break-all" data-testid={`secret-${f.label}`}>
                  {f.value}
                </code>
              )}
            </div>
          ))}
        </div>
        <DialogFooter>
          {download && (
            <Button variant="outline" onClick={() => downloadText(download.filename, download.content)}>
              <Download /> Download
            </Button>
          )}
          <Button onClick={onClose}>I have saved it</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
