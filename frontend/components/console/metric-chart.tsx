"use client";

import { Area, AreaChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { DatedValue } from "@/lib/types";
import { formatTime } from "@/lib/format";

export interface MetricChartProps {
  data: DatedValue[];
  height?: number;
  variant?: "line" | "area";
  unit?: string;
  color?: string;
  yDomain?: [number | "auto", number | "auto"];
  label?: string;
  formatValue?: (v: number) => string;
}

/** Time-series chart used by the dashboard, EC2 monitoring and the CloudWatch metrics explorer. */
export function MetricChart({
  data,
  height = 220,
  variant = "line",
  unit,
  color = "var(--chart-1)",
  yDomain,
  label,
  formatValue = (v) => (Math.abs(v) >= 1000 ? v.toLocaleString(undefined, { maximumFractionDigits: 0 }) : v.toFixed(1)),
}: MetricChartProps) {
  const rows = data.map((d) => ({ t: new Date(d.ts).getTime(), v: d.value }));
  if (rows.length === 0) {
    return (
      <div style={{ height }} className="flex items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground">
        No datapoints in this time range
      </div>
    );
  }
  const common = (
    <>
      <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
      <XAxis
        dataKey="t"
        type="number"
        scale="time"
        domain={["dataMin", "dataMax"]}
        tickFormatter={(t: number) => formatTime(new Date(t).toISOString()).slice(0, 5)}
        tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
        stroke="var(--border)"
      />
      <YAxis
        domain={yDomain ?? ["auto", "auto"]}
        tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
        stroke="var(--border)"
        width={48}
        tickFormatter={(v: number) => formatValue(v)}
      />
      <Tooltip
        contentStyle={{ background: "var(--popover)", border: "1px solid var(--border)", borderRadius: 6, fontSize: 12 }}
        labelFormatter={(t) => formatTime(new Date(Number(t)).toISOString())}
        formatter={(v) => [`${formatValue(Number(v))}${unit ? ` ${unit}` : ""}`, label ?? "Value"]}
      />
    </>
  );
  return (
    <div role="img" aria-label={`${label ?? "Metric"} chart with ${rows.length} datapoints`} style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        {variant === "area" ? (
          <AreaChart data={rows} margin={{ top: 5, right: 8, bottom: 0, left: 0 }}>
            {common}
            <Area type="monotone" dataKey="v" stroke={color} fill={color} fillOpacity={0.15} strokeWidth={1.5} isAnimationActive={false} />
          </AreaChart>
        ) : (
          <LineChart data={rows} margin={{ top: 5, right: 8, bottom: 0, left: 0 }}>
            {common}
            <Line type="monotone" dataKey="v" stroke={color} strokeWidth={1.5} dot={false} isAnimationActive={false} />
          </LineChart>
        )}
      </ResponsiveContainer>
    </div>
  );
}
