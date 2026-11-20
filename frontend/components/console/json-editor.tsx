"use client";

import { useState } from "react";
import Editor, { loader } from "@monaco-editor/react";
import { useTheme } from "next-themes";
import { CheckCircle2, Loader2, ShieldCheck, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { api, errorMessage } from "@/lib/api/client";
import { parsePolicyJson } from "@/lib/validation";
import type { ValidatePolicyResponse } from "@/lib/types";

// Monaco is served from the console itself (copied to /public/monaco by scripts/copy-monaco.mjs), not a CDN.
if (typeof window !== "undefined") {
  loader.config({ paths: { vs: "/monaco/vs" } });
}

export interface JsonEditorProps {
  value: string;
  onChange: (value: string) => void;
  height?: number | string;
  readOnly?: boolean;
  /** Show the "Validate policy" button which calls POST /iam/validate-policy. */
  validatePolicy?: boolean;
  label: string;
}

type ValidationState =
  | { kind: "idle" }
  | { kind: "pending" }
  | { kind: "ok" }
  | { kind: "errors"; errors: string[] };

export function JsonEditor({ value, onChange, height = 320, readOnly, validatePolicy, label }: JsonEditorProps) {
  const { resolvedTheme } = useTheme();
  const [validation, setValidation] = useState<ValidationState>({ kind: "idle" });
  const parsed = parsePolicyJson(value);

  const runValidate = async () => {
    if (parsed.error || !parsed.value) {
      setValidation({ kind: "errors", errors: [parsed.error ?? "Invalid JSON"] });
      return;
    }
    setValidation({ kind: "pending" });
    try {
      const r = await api<ValidatePolicyResponse>("/iam/validate-policy", {
        method: "POST",
        body: { policy_document: parsed.value },
      });
      if (r.valid) setValidation({ kind: "ok" });
      else
        setValidation({
          kind: "errors",
          errors: (r.errors ?? []).map((e) => (typeof e === "string" ? e : e.path ? `${e.path}: ${e.message}` : e.message)),
        });
    } catch (err) {
      setValidation({ kind: "errors", errors: [errorMessage(err)] });
    }
  };

  return (
    <div className="grid gap-2">
      <div className="overflow-hidden rounded-md border" role="group" aria-label={label}>
        <Editor
          height={height}
          language="json"
          value={value}
          theme={resolvedTheme === "dark" ? "vs-dark" : "light"}
          onChange={(v) => {
            onChange(v ?? "");
            if (validation.kind !== "idle") setValidation({ kind: "idle" });
          }}
          loading={<div className="p-3 text-sm text-muted-foreground">Loading editor…</div>}
          options={{
            readOnly,
            minimap: { enabled: false },
            fontSize: 12,
            scrollBeyondLastLine: false,
            tabSize: 2,
            automaticLayout: true,
            ariaLabel: label,
            wordWrap: "on",
          }}
        />
      </div>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        {parsed.error ? (
          <span className="flex items-center gap-1 text-destructive" role="status">
            <XCircle className="size-3.5" aria-hidden /> {parsed.error}
          </span>
        ) : (
          <span className="flex items-center gap-1 text-muted-foreground" role="status">
            <CheckCircle2 className="size-3.5" aria-hidden /> Valid JSON
          </span>
        )}
        {validatePolicy && (
          <Button type="button" size="xs" variant="outline" onClick={() => void runValidate()} disabled={validation.kind === "pending"}>
            {validation.kind === "pending" ? <Loader2 className="animate-spin" /> : <ShieldCheck />} Validate policy
          </Button>
        )}
        {validation.kind === "ok" && (
          <span className="text-emerald-600 dark:text-emerald-400" role="status">
            Policy is valid.
          </span>
        )}
      </div>
      {validation.kind === "errors" && (
        <ul role="alert" className="list-disc rounded-md border border-destructive/30 bg-destructive/5 py-2 pr-3 pl-7 text-xs text-destructive">
          {validation.errors.length === 0 ? <li>Policy is invalid.</li> : validation.errors.map((e, i) => <li key={i}>{e}</li>)}
        </ul>
      )}
    </div>
  );
}

export const prettyJson = (v: unknown) => JSON.stringify(v ?? {}, null, 2);
