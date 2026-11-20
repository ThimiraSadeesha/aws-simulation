"use client";

import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export interface KV {
  key: string;
  value: string;
}

export function kvFromRecord(r: Record<string, string> | undefined | null): KV[] {
  return Object.entries(r ?? {}).map(([key, value]) => ({ key, value }));
}

export function kvToRecord(rows: KV[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) if (r.key.trim()) out[r.key.trim()] = r.value;
  return out;
}

/** Editable key/value list (tags, dimensions, metadata). */
export function KeyValueEditor({
  value,
  onChange,
  keyLabel = "Key",
  valueLabel = "Value",
  addLabel = "Add tag",
  max = 50,
}: {
  value: KV[];
  onChange: (rows: KV[]) => void;
  keyLabel?: string;
  valueLabel?: string;
  addLabel?: string;
  max?: number;
}) {
  const update = (i: number, patch: Partial<KV>) => onChange(value.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <div className="grid gap-2">
      {value.length > 0 && (
        <div className="grid grid-cols-[1fr_1fr_auto] gap-2 text-xs text-muted-foreground">
          <span>{keyLabel}</span>
          <span>{valueLabel}</span>
          <span className="w-7" />
        </div>
      )}
      {value.map((row, i) => (
        <div key={i} className="grid grid-cols-[1fr_1fr_auto] gap-2">
          <Input aria-label={`${keyLabel} ${i + 1}`} value={row.key} onChange={(e) => update(i, { key: e.target.value })} className="h-8" />
          <Input aria-label={`${valueLabel} ${i + 1}`} value={row.value} onChange={(e) => update(i, { value: e.target.value })} className="h-8" />
          <Button type="button" size="icon-sm" variant="ghost" aria-label={`Remove ${keyLabel.toLowerCase()} ${i + 1}`} onClick={() => onChange(value.filter((_, j) => j !== i))}>
            <Trash2 />
          </Button>
        </div>
      ))}
      <div>
        <Button type="button" size="sm" variant="outline" disabled={value.length >= max} onClick={() => onChange([...value, { key: "", value: "" }])}>
          <Plus /> {addLabel}
        </Button>
      </div>
    </div>
  );
}
