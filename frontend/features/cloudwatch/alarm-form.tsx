"use client";

import { useId, useMemo, useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormError, FormField, NativeSelect } from "@/components/console/form-field";
import { errorMessage } from "@/lib/api/client";
import { COMPARISON_OPERATORS, STATISTICS, hasErrors, validateAlarm, type AlarmForm, type FieldErrors } from "@/lib/validation";
import type { AlarmInput, ComparisonOperator, MetricDescriptor, Statistic } from "@/lib/types";

export const EMPTY_ALARM_FORM: AlarmForm = {
  alarm_name: "",
  namespace: "CloudSim/EC2",
  metric_name: "CPUUtilization",
  dimension_key: "",
  dimension_value: "",
  statistic: "Average",
  period: "60",
  evaluation_periods: "1",
  threshold: "80",
  comparison_operator: "GreaterThanThreshold",
  alarm_actions: "",
};

const OPERATOR_LABEL: Record<string, string> = {
  GreaterThanThreshold: "> threshold",
  GreaterThanOrEqualToThreshold: ">= threshold",
  LessThanThreshold: "< threshold",
  LessThanOrEqualToThreshold: "<= threshold",
};

/** Convert validated form values to the POST /cloudwatch/alarms body. */
export function alarmFormToInput(f: AlarmForm): AlarmInput {
  const actions = f.alarm_actions.split(/[\s,]+/).filter(Boolean);
  return {
    alarm_name: f.alarm_name.trim(),
    namespace: f.namespace.trim(),
    metric_name: f.metric_name.trim(),
    dimensions: f.dimension_key ? { [f.dimension_key.trim()]: f.dimension_value.trim() } : {},
    statistic: f.statistic as Statistic,
    period: Number(f.period),
    evaluation_periods: Number(f.evaluation_periods),
    threshold: Number(f.threshold),
    comparison_operator: f.comparison_operator as ComparisonOperator,
    ...(actions.length ? { alarm_actions: actions } : {}),
  };
}

export interface CreateAlarmFormProps {
  onSubmit: (input: AlarmInput) => Promise<unknown>;
  metrics?: MetricDescriptor[];
  onCancel?: () => void;
  initial?: Partial<AlarmForm>;
}

/**
 * CloudWatch alarm form with client-side validation (lib/validation.ts → validateAlarm).
 * Invalid input shows inline role="alert" messages and never calls onSubmit.
 * API errors thrown by onSubmit are shown inline.
 */
export function CreateAlarmForm({ onSubmit, metrics = [], onCancel, initial }: CreateAlarmFormProps) {
  const [f, setF] = useState<AlarmForm>({ ...EMPTY_ALARM_FORM, ...initial });
  const [errors, setErrors] = useState<FieldErrors>({});
  const [apiError, setApiError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const uid = useId();

  const namespaces = useMemo(() => [...new Set(metrics.map((m) => m.namespace))].sort(), [metrics]);
  const metricNames = useMemo(
    () => [...new Set(metrics.filter((m) => m.namespace === f.namespace).map((m) => m.metric_name))].sort(),
    [metrics, f.namespace],
  );
  const dimensionOptions = useMemo(() => {
    const out = new Map<string, string>();
    for (const m of metrics) {
      if (m.namespace !== f.namespace || m.metric_name !== f.metric_name) continue;
      for (const [k, v] of Object.entries(m.dimensions ?? {})) out.set(`${k}=${v}`, k);
    }
    return [...out.keys()];
  }, [metrics, f.namespace, f.metric_name]);

  const set = (k: keyof AlarmForm) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF((s) => ({ ...s, [k]: e.target.value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setApiError(null);
    const v = validateAlarm(f);
    setErrors(v);
    if (hasErrors(v)) return;
    setPending(true);
    try {
      await onSubmit(alarmFormToInput(f));
    } catch (err) {
      setApiError(errorMessage(err));
    } finally {
      setPending(false);
    }
  };

  return (
    <form onSubmit={submit} className="grid gap-3" noValidate aria-label="Create alarm">
      <FormError message={apiError} />
      <FormField label="Alarm name" error={errors.alarm_name}>
        {(p) => <Input {...p} value={f.alarm_name} onChange={set("alarm_name")} placeholder="high-cpu" />}
      </FormField>
      <div className="grid gap-3 sm:grid-cols-2">
        <FormField label="Namespace" error={errors.namespace}>
          {(p) => (
            <>
              <Input {...p} list={`${uid}-ns`} value={f.namespace} onChange={set("namespace")} />
              <datalist id={`${uid}-ns`}>
                {namespaces.map((n) => (
                  <option key={n} value={n} />
                ))}
              </datalist>
            </>
          )}
        </FormField>
        <FormField label="Metric name" error={errors.metric_name}>
          {(p) => (
            <>
              <Input {...p} list={`${uid}-metric`} value={f.metric_name} onChange={set("metric_name")} />
              <datalist id={`${uid}-metric`}>
                {metricNames.map((n) => (
                  <option key={n} value={n} />
                ))}
              </datalist>
            </>
          )}
        </FormField>
      </div>
      {dimensionOptions.length > 0 && (
        <FormField label="Pick a known dimension" hint="Fills the dimension name and value below.">
          {(p) => (
            <NativeSelect
              {...p}
              value={f.dimension_key ? `${f.dimension_key}=${f.dimension_value}` : ""}
              onChange={(e) => {
                const [k, ...rest] = e.target.value.split("=");
                setF((s) => ({ ...s, dimension_key: e.target.value ? k : "", dimension_value: e.target.value ? rest.join("=") : "" }));
              }}
            >
              <option value="">None</option>
              {dimensionOptions.map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <FormField label="Dimension name" hint="e.g. InstanceId">
          {(p) => <Input {...p} value={f.dimension_key} onChange={set("dimension_key")} />}
        </FormField>
        <FormField label="Dimension value" error={errors.dimension_value}>
          {(p) => <Input {...p} value={f.dimension_value} onChange={set("dimension_value")} />}
        </FormField>
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <FormField label="Statistic" error={errors.statistic}>
          {(p) => (
            <NativeSelect {...p} value={f.statistic} onChange={set("statistic")}>
              {STATISTICS.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Period (seconds)" error={errors.period}>
          {(p) => <Input {...p} type="number" value={f.period} onChange={set("period")} />}
        </FormField>
        <FormField label="Evaluation periods" error={errors.evaluation_periods}>
          {(p) => <Input {...p} type="number" value={f.evaluation_periods} onChange={set("evaluation_periods")} />}
        </FormField>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <FormField label="Comparison operator" error={errors.comparison_operator}>
          {(p) => (
            <NativeSelect {...p} value={f.comparison_operator} onChange={set("comparison_operator")}>
              {COMPARISON_OPERATORS.map((o) => (
                <option key={o} value={o}>
                  {o} ({OPERATOR_LABEL[o]})
                </option>
              ))}
            </NativeSelect>
          )}
        </FormField>
        <FormField label="Threshold" error={errors.threshold}>
          {(p) => <Input {...p} type="number" step="any" value={f.threshold} onChange={set("threshold")} />}
        </FormField>
      </div>
      <FormField
        label="Alarm actions"
        error={errors.alarm_actions}
        hint="Optional. Space or comma separated ARNs – Auto Scaling policies (arn:cloudsim:autoscaling:…) or SNS topics (arn:cloudsim:sns:…)."
      >
        {(p) => <Input {...p} value={f.alarm_actions} onChange={set("alarm_actions")} placeholder="arn:cloudsim:sns:…" />}
      </FormField>
      <div className="flex justify-end gap-2 pt-1">
        {onCancel && (
          <Button type="button" variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" disabled={pending}>
          {pending && <Loader2 className="animate-spin" />} Create alarm
        </Button>
      </div>
    </form>
  );
}
