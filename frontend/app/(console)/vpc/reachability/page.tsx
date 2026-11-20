"use client";

import { useState } from "react";
import { ArrowDown, CheckCircle2, Play, ShieldX, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/console/page-header";
import { Section } from "@/components/console/detail-grid";
import { StatusBadge } from "@/components/console/status-badge";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { useQuery } from "@tanstack/react-query";
import { apiAll, errorMessage } from "@/lib/api/client";
import { parseIpv4 } from "@/lib/validation";
import type { Instance, ReachabilityRequest } from "@/lib/types";
import { useReachability } from "@/features/vpc/api";
import { PROTOCOL_OPTIONS } from "@/features/vpc/components/common";
import { cn } from "@/lib/utils";

function EndpointPicker({
  label,
  kind,
  setKind,
  value,
  setValue,
  instances,
  error,
}: {
  label: string;
  kind: string;
  setKind: (k: string) => void;
  value: string;
  setValue: (v: string) => void;
  instances: Instance[];
  error: string | null;
}) {
  return (
    <fieldset className="grid gap-2 rounded-md border p-3">
      <legend className="px-1 text-xs font-medium">{label}</legend>
      <FormField label={`${label} type`}>
        {(p) => (
          <NativeSelect
            {...p}
            value={kind}
            onChange={(e) => {
              setKind(e.target.value);
              setValue("");
            }}
          >
            <option value="instance">EC2 instance</option>
            <option value="internet">Internet</option>
            <option value="ip">IP address</option>
          </NativeSelect>
        )}
      </FormField>
      {kind === "instance" && (
        <FormField label={`${label} instance`} error={error}>
          {(p) => (
            <NativeSelect {...p} value={value} onChange={(e) => setValue(e.target.value)}>
              <option value="">Select an instance</option>
              {instances.map((i) => (
                <option key={i.instance_id} value={i.instance_id}>
                  {i.instance_id} {i.name ? `(${i.name})` : ""} · {i.state} · {i.private_ip}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
      )}
      {kind === "ip" && (
        <FormField label={`${label} IP address`} error={error}>
          {(p) => <Input {...p} className="font-mono" value={value} onChange={(e) => setValue(e.target.value)} placeholder="10.0.1.10" />}
        </FormField>
      )}
    </fieldset>
  );
}

function endpointValue(kind: string, value: string) {
  return kind === "internet" ? "internet" : value.trim();
}
function endpointError(kind: string, value: string) {
  if (kind === "internet") return null;
  if (kind === "instance") return value ? null : "Choose an instance.";
  return parseIpv4(value) === null ? "Enter a valid IPv4 address." : null;
}

export default function ReachabilityPage() {
  const instances = useQuery({
    queryKey: ["ec2", "instances", "all", { reachability: "1" }],
    queryFn: ({ signal }) => apiAll<Instance>("/ec2/instances", {}, signal),
  });
  const insts = (instances.data ?? []).filter((i) => i.state !== "terminated");
  const [srcKind, setSrcKind] = useState("internet");
  const [src, setSrc] = useState("");
  const [dstKind, setDstKind] = useState("instance");
  const [dst, setDst] = useState("");
  const [protocol, setProtocol] = useState<ReachabilityRequest["protocol"]>("tcp");
  const [port, setPort] = useState("22");
  const [touched, setTouched] = useState(false);
  const analyze = useReachability();

  const srcErr = endpointError(srcKind, src);
  const dstErr = endpointError(dstKind, dst);
  const portless = protocol === "-1" || protocol === "icmp";
  const portN = Number(port);
  const portErr = portless || (Number.isInteger(portN) && portN >= 0 && portN <= 65535) ? null : "Port must be 0–65535.";
  const r = analyze.data;

  return (
    <div className="grid gap-4">
      <PageHeader
        title="Reachability Analyzer"
        description="Trace a simulated packet hop by hop through security groups, network ACLs, route tables and gateways."
      />
      <Section title="Path">
        <form
          noValidate
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            setTouched(true);
            if (srcErr || dstErr || portErr) return;
            analyze.mutate({
              source: endpointValue(srcKind, src),
              destination: endpointValue(dstKind, dst),
              protocol,
              port: portless ? 0 : portN,
            });
          }}
        >
          <div className="grid gap-3 md:grid-cols-2">
            <EndpointPicker label="Source" kind={srcKind} setKind={setSrcKind} value={src} setValue={setSrc} instances={insts} error={touched ? srcErr : null} />
            <EndpointPicker label="Destination" kind={dstKind} setKind={setDstKind} value={dst} setValue={setDst} instances={insts} error={touched ? dstErr : null} />
          </div>
          <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
            <FormField label="Protocol">
              {(p) => (
                <NativeSelect {...p} value={protocol} onChange={(e) => setProtocol(e.target.value as ReachabilityRequest["protocol"])}>
                  {PROTOCOL_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </FormField>
            <FormField label="Destination port" error={touched ? portErr : null}>
              {(p) => <Input {...p} type="number" disabled={portless} value={portless ? "" : port} onChange={(e) => setPort(e.target.value)} />}
            </FormField>
            <Button type="submit" disabled={analyze.isPending}>
              <Play /> Analyze path
            </Button>
          </div>
        </form>
      </Section>

      {analyze.isError && <FormError message={errorMessage(analyze.error)} />}

      {r && (
        <div className="grid gap-4" aria-live="polite">
          <div
            role="status"
            className={cn(
              "flex items-start gap-3 rounded-lg border p-4",
              r.reachable ? "border-emerald-500/40 bg-emerald-500/10" : "border-red-500/40 bg-red-500/10",
            )}
          >
            {r.reachable ? <CheckCircle2 className="mt-0.5 size-5 text-emerald-600" aria-hidden /> : <XCircle className="mt-0.5 size-5 text-red-600" aria-hidden />}
            <div className="grid gap-1">
              <p className="font-semibold">{r.reachable ? "Reachable" : "Not reachable"}</p>
              <p className="text-sm">{r.explanation}</p>
              {typeof r.simulated_latency_ms === "number" && (
                <p className="text-xs text-muted-foreground">Simulated latency: {r.simulated_latency_ms.toFixed(1)} ms</p>
              )}
            </div>
          </div>

          {r.blocked_by && (
            <div className="flex items-start gap-3 rounded-lg border-2 border-red-500/60 bg-card p-4" data-testid="blocked-by">
              <ShieldX className="mt-0.5 size-5 text-red-600" aria-hidden />
              <div className="grid gap-0.5">
                <p className="text-sm font-semibold">Blocked by {r.blocked_by.type.replace(/_/g, " ")}</p>
                <p className="font-mono text-sm">{r.blocked_by.component}</p>
                <p className="text-sm text-muted-foreground">{r.blocked_by.detail}</p>
              </div>
            </div>
          )}

          <Section title={`Hops (${r.hops.length})`}>
            {r.hops.length === 0 ? (
              <p className="text-sm text-muted-foreground">No hops evaluated.</p>
            ) : (
              <ol className="grid gap-0" aria-label="Hop-by-hop path">
                {r.hops.map((h, i) => {
                  const blocked = r.blocked_by?.component === h.component && h.result !== "allow";
                  return (
                    <li key={`${h.component}-${i}`} className="grid gap-0">
                      <div className={cn("flex flex-wrap items-center gap-3 rounded-md border p-3", blocked && "border-red-500/60 bg-red-500/5")}>
                        <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold">{i + 1}</span>
                        <div className="grid min-w-0 flex-1 gap-0.5">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="font-mono text-sm">{h.component}</span>
                            <span className="text-xs text-muted-foreground">
                              {h.type.replace(/_/g, " ")}
                              {h.direction ? ` · ${h.direction}` : ""}
                            </span>
                          </div>
                          <p className="text-xs text-muted-foreground">{h.detail}</p>
                        </div>
                        <StatusBadge status={h.result} />
                      </div>
                      {i < r.hops.length - 1 && <ArrowDown className="mx-auto my-1 size-4 text-muted-foreground" aria-hidden />}
                    </li>
                  );
                })}
              </ol>
            )}
          </Section>
        </div>
      )}
    </div>
  );
}
