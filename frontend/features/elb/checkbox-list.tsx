"use client";

import { Checkbox } from "@/components/ui/checkbox";

export interface CheckOption {
  value: string;
  label: string;
  hint?: string;
}

/** Accessible multi-select rendered as a scrollable checkbox list (used by ELB / Auto Scaling forms). */
export function CheckboxList({
  id,
  options,
  value,
  onChange,
  emptyText = "None available",
  label,
}: {
  id?: string;
  options: CheckOption[];
  value: string[];
  onChange: (v: string[]) => void;
  emptyText?: string;
  label: string;
}) {
  if (options.length === 0) return <p className="text-xs text-muted-foreground">{emptyText}</p>;
  return (
    <div id={id} role="group" aria-label={label} className="max-h-40 overflow-y-auto rounded-md border p-1">
      {options.map((o) => {
        const checked = value.includes(o.value);
        const cid = `${id ?? label}-${o.value}`;
        return (
          <label key={o.value} htmlFor={cid} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm hover:bg-muted/50">
            <Checkbox
              id={cid}
              checked={checked}
              onCheckedChange={(c) => onChange(c ? [...value, o.value] : value.filter((v) => v !== o.value))}
            />
            <span className="font-mono text-xs">{o.value}</span>
            {o.label && o.label !== o.value && <span className="truncate text-xs text-muted-foreground">{o.label}</span>}
            {o.hint && <span className="ml-auto text-xs text-muted-foreground">{o.hint}</span>}
          </label>
        );
      })}
    </div>
  );
}
