"use client";

import { useMemo, useState } from "react";
import { Loader2, RefreshCw, Send } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { Section } from "@/components/console/detail-grid";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { KeyValueEditor, kvToRecord, type KV } from "@/components/console/key-value-editor";
import { MetricChart } from "@/components/console/metric-chart";
import { PageHeader } from "@/components/console/page-header";
import { ErrorState } from "@/components/console/states";
import { errorMessage } from "@/lib/api/client";
import { STATISTICS, validateNamespaceForPublish } from "@/lib/validation";
import type { Statistic } from "@/lib/types";
import { TIME_RANGES, useMetricData, useMetricList } from "./metrics-api";
import { usePublishMetric } from "./api";

const PERIODS = [
  { value: 60, label: "1 minute" },
  { value: 300, label: "5 minutes" },
  { value: 900, label: "15 minutes" },
  { value: 3600, label: "1 hour" },
];

const UNITS = ["None", "Percent", "Count", "Bytes", "Seconds", "Milliseconds", "Count/Second", "Bytes/Second"];

export function MetricsExplorer() {
  const metrics = useMetricList();
  const [ns, setNs] = useState("");
  const [metric, setMetric] = useState("");
  const [dimension, setDimension] = useState("");
  const [period, setPeriod] = useState(60);
  const [stat, setStat] = useState<Statistic>("Average");
  const [range, setRange] = useState(60);
  const [auto, setAuto] = useState(true);

  const list = useMemo(() => metrics.data ?? [], [metrics.data]);
  const namespaces = useMemo(() => [...new Set(list.map((m) => m.namespace))].sort(), [list]);
  const effNs = ns || namespaces[0] || "";
  const metricNames = useMemo(() => [...new Set(list.filter((m) => m.namespace === effNs).map((m) => m.metric_name))].sort(), [list, effNs]);
  const effMetric = metricNames.includes(metric) ? metric : (metricNames[0] ?? "");
  const dimensions = useMemo(() => {
    const s = new Set<string>();
    for (const m of list) {
      if (m.namespace !== effNs || m.metric_name !== effMetric) continue;
      const entries = Object.entries(m.dimensions ?? {});
      // The query API accepts a single `dimension=Name:Value`.
      for (const [k, v] of entries) s.add(`${k}:${v}`);
    }
    return [...s].sort();
  }, [list, effNs, effMetric]);
  const effDim = dimension === "__none" ? "" : dimensions.includes(dimension) ? dimension : (dimensions[0] ?? "");

  const data = useMetricData(
    effNs && effMetric ? { namespace: effNs, metric_name: effMetric, dimension: effDim || undefined, period, stat, rangeMinutes: range } : null,
    { refetchInterval: auto ? 30_000 : false },
  );

  return (
    <div className="grid gap-4">
      <PageHeader
        title="Metrics"
        description="Explore built-in simulator metrics (CloudSim/EC2, CloudSim/S3, CloudSim/AutoScaling) and your custom metrics."
        breadcrumbs={[{ label: "CloudWatch" }, { label: "Metrics" }]}
      />
      <Section
        title="Metrics explorer"
        actions={
          <>
            <div className="flex items-center gap-2">
              <Switch id="cw-auto" checked={auto} onCheckedChange={setAuto} />
              <Label htmlFor="cw-auto" className="text-xs font-normal">
                Auto refresh (30 s)
              </Label>
            </div>
            <Button size="icon-sm" variant="ghost" aria-label="Refresh chart" onClick={() => void data.refetch()}>
              {data.isFetching ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            </Button>
          </>
        }
      >
        {metrics.isError ? (
          <ErrorState error={metrics.error} onRetry={() => void metrics.refetch()} />
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-6">
              <FormField label="Namespace" className="lg:col-span-2">
                {(p) => (
                  <NativeSelect
                    {...p}
                    value={effNs}
                    onChange={(e) => {
                      setNs(e.target.value);
                      setMetric("");
                      setDimension("");
                    }}
                    disabled={metrics.isLoading || namespaces.length === 0}
                  >
                    {namespaces.length === 0 && <option value="">{metrics.isLoading ? "Loading…" : "No metrics yet"}</option>}
                    {namespaces.map((n) => (
                      <option key={n} value={n}>
                        {n}
                      </option>
                    ))}
                  </NativeSelect>
                )}
              </FormField>
              <FormField label="Metric" className="lg:col-span-2">
                {(p) => (
                  <NativeSelect
                    {...p}
                    value={effMetric}
                    onChange={(e) => {
                      setMetric(e.target.value);
                      setDimension("");
                    }}
                    disabled={metricNames.length === 0}
                  >
                    {metricNames.map((n) => (
                      <option key={n} value={n}>
                        {n}
                      </option>
                    ))}
                  </NativeSelect>
                )}
              </FormField>
              <FormField label="Dimension" className="lg:col-span-2">
                {(p) => (
                  <NativeSelect {...p} value={effDim || "__none"} onChange={(e) => setDimension(e.target.value)}>
                    <option value="__none">All (no dimension filter)</option>
                    {dimensions.map((d) => (
                      <option key={d} value={d}>
                        {d}
                      </option>
                    ))}
                  </NativeSelect>
                )}
              </FormField>
              <FormField label="Statistic" className="lg:col-span-2">
                {(p) => (
                  <NativeSelect {...p} value={stat} onChange={(e) => setStat(e.target.value as Statistic)}>
                    {STATISTICS.map((s) => (
                      <option key={s}>{s}</option>
                    ))}
                  </NativeSelect>
                )}
              </FormField>
              <FormField label="Period" className="lg:col-span-2">
                {(p) => (
                  <NativeSelect {...p} value={period} onChange={(e) => setPeriod(Number(e.target.value))}>
                    {PERIODS.map((x) => (
                      <option key={x.value} value={x.value}>
                        {x.label}
                      </option>
                    ))}
                  </NativeSelect>
                )}
              </FormField>
              <FormField label="Time range" className="lg:col-span-2">
                {(p) => (
                  <NativeSelect {...p} value={range} onChange={(e) => setRange(Number(e.target.value))}>
                    {TIME_RANGES.map((r) => (
                      <option key={r.minutes} value={r.minutes}>
                        Last {r.label}
                      </option>
                    ))}
                  </NativeSelect>
                )}
              </FormField>
            </div>
            <div className="mt-4">
              {data.isError ? (
                <ErrorState error={data.error} onRetry={() => void data.refetch()} />
              ) : !effMetric ? (
                <div className="flex h-72 items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground">
                  {metrics.isLoading ? "Loading metrics…" : "No metrics available yet. Launch an instance or publish a custom metric."}
                </div>
              ) : (
                <>
                  <div className="mb-1 text-xs text-muted-foreground">
                    {data.data?.label ?? `${effNs} · ${effMetric}`} · {stat} per {period}s
                  </div>
                  <MetricChart data={data.data?.datapoints ?? []} height={300} label={effMetric} unit={effMetric.includes("Utilization") ? "%" : undefined} />
                </>
              )}
            </div>
          </>
        )}
      </Section>
      <PublishMetricForm />
    </div>
  );
}

function PublishMetricForm() {
  const publish = usePublishMetric();
  const [ns, setNs] = useState("MyApp");
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [unit, setUnit] = useState("None");
  const [dims, setDims] = useState<KV[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    const nsErr = validateNamespaceForPublish(ns);
    if (nsErr) errs.namespace = nsErr;
    if (!name.trim()) errs.metric_name = "Metric name is required.";
    if (value.trim() === "" || Number.isNaN(Number(value))) errs.value = "Value must be a number.";
    if (dims.some((d) => !d.key.trim() && d.value)) errs.dimensions = "Dimension names must not be empty.";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    setErr(null);
    try {
      const dimensions = kvToRecord(dims);
      await publish.mutateAsync({
        namespace: ns.trim(),
        metric_data: [
          {
            metric_name: name.trim(),
            value: Number(value),
            ...(unit !== "None" ? { unit } : {}),
            ...(Object.keys(dimensions).length ? { dimensions } : {}),
          },
        ],
      });
      toast.success(`Published ${ns}/${name} = ${value}`);
      setValue("");
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  };

  return (
    <Section title="Publish custom metric">
      <form onSubmit={submit} className="grid gap-3" noValidate>
        <FormError message={err} />
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <FormField label="Namespace" error={errors.namespace} hint="Must not start with CloudSim/">
            {(p) => <Input {...p} value={ns} onChange={(e) => setNs(e.target.value)} />}
          </FormField>
          <FormField label="Metric name" error={errors.metric_name}>
            {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} placeholder="OrdersPlaced" />}
          </FormField>
          <FormField label="Value" error={errors.value}>
            {(p) => <Input {...p} type="number" step="any" value={value} onChange={(e) => setValue(e.target.value)} />}
          </FormField>
          <FormField label="Unit">
            {(p) => (
              <NativeSelect {...p} value={unit} onChange={(e) => setUnit(e.target.value)}>
                {UNITS.map((u) => (
                  <option key={u}>{u}</option>
                ))}
              </NativeSelect>
            )}
          </FormField>
        </div>
        <div className="grid gap-1">
          <span className="text-xs font-medium">Dimensions</span>
          <KeyValueEditor value={dims} onChange={setDims} keyLabel="Dimension name" valueLabel="Dimension value" addLabel="Add dimension" max={10} />
          {errors.dimensions && (
            <p role="alert" className="text-xs text-destructive">
              {errors.dimensions}
            </p>
          )}
        </div>
        <div>
          <Button type="submit" disabled={publish.isPending}>
            {publish.isPending ? <Loader2 className="animate-spin" /> : <Send />} Publish datapoint
          </Button>
        </div>
      </form>
    </Section>
  );
}
