"use client";

import type { RouteTable, Subnet, Topology } from "@/lib/types";

/**
 * Pure SVG topology diagram (no graph library). Layout is computed:
 *   Internet → IGW node(s) on the VPC's top edge → optional load balancer band → subnet grid.
 * Subnets are coloured public (emerald) / private (blue); instances and NAT gateways are chips inside
 * their subnet. Connectors: subnet → IGW for 0.0.0.0/0 → igw-* routes (solid), subnet → NAT for → nat-*
 * routes (dashed), load balancer → its subnets (dotted).
 */

const W = 1000;
const PAD = 24;
const GAP = 20;
const CHIP_H = 24;
const CHIP_GAP = 6;
const SUB_HEADER = 62;

function trunc(s: string, n: number) {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

export function routeTableFor(subnet: Subnet, tables: RouteTable[]): RouteTable | undefined {
  return (
    tables.find((t) => t.route_table_id === subnet.route_table_id) ??
    tables.find((t) => t.subnet_ids?.includes(subnet.subnet_id)) ??
    tables.find((t) => t.is_main)
  );
}

const STATE_FILL: Record<string, string> = {
  running: "fill-emerald-500",
  available: "fill-emerald-500",
  active: "fill-emerald-500",
  pending: "fill-amber-500",
  stopping: "fill-amber-500",
  rebooting: "fill-amber-500",
  "shutting-down": "fill-amber-500",
  provisioning: "fill-amber-500",
  deleting: "fill-amber-500",
  stopped: "fill-slate-400",
  terminated: "fill-slate-300",
  deleted: "fill-slate-300",
};

interface Chip {
  kind: "instance" | "nat";
  id: string;
  title: string;
  sub: string;
  state: string;
}

interface Box {
  subnet: Subnet;
  x: number;
  y: number;
  w: number;
  h: number;
  chips: Chip[];
}

export function layoutTopology(t: Topology) {
  const subnets = [...t.subnets].sort((a, b) => a.cidr_block.localeCompare(b.cidr_block, undefined, { numeric: true }));
  const cols = Math.max(1, Math.min(3, subnets.length));
  const innerW = W - PAD * 2 - 2 * PAD;
  const boxW = (innerW - GAP * (cols - 1)) / cols;
  const lbs = t.load_balancers ?? [];
  const igws = t.internet_gateways.filter((g) => g.vpc_id === t.vpc.vpc_id || g.state === "attached");

  const vpcTop = 110;
  const lbBand = lbs.length > 0 ? 64 : 0;
  let y = vpcTop + 48 + lbBand;

  const boxes: Box[] = [];
  for (let r = 0; r < Math.ceil(subnets.length / cols); r++) {
    const row = subnets.slice(r * cols, r * cols + cols);
    const rowBoxes = row.map((s, c) => {
      const chips: Chip[] = [
        ...t.nat_gateways
          .filter((n) => n.subnet_id === s.subnet_id && n.state !== "deleted")
          .map((n) => ({ kind: "nat" as const, id: n.nat_gateway_id, title: n.name || n.nat_gateway_id, sub: n.public_ip || n.state, state: n.state })),
        ...t.instances
          .filter((i) => i.subnet_id === s.subnet_id)
          .map((i) => ({
            kind: "instance" as const,
            id: i.instance_id,
            title: i.name || i.instance_id,
            sub: [i.private_ip, i.public_ip].filter(Boolean).join(" · "),
            state: i.state,
          })),
      ];
      const h = Math.max(100, SUB_HEADER + chips.length * (CHIP_H + CHIP_GAP) + 10);
      return { subnet: s, x: PAD * 2 + c * (boxW + GAP), y, w: boxW, h, chips };
    });
    const rowH = Math.max(...rowBoxes.map((b) => b.h));
    rowBoxes.forEach((b) => (b.h = rowH));
    boxes.push(...rowBoxes);
    y += rowH + GAP;
  }
  if (subnets.length === 0) y += 80;
  const vpcBottom = y + 4;
  const height = vpcBottom + PAD;

  // IGW nodes centred on the VPC top edge
  const igwW = 170;
  const igwNodes = igws.map((g, i) => {
    const total = igws.length * igwW + (igws.length - 1) * GAP;
    const x0 = W / 2 - total / 2 + i * (igwW + GAP);
    return { igw: g, x: x0, y: vpcTop - 18, w: igwW, h: 36 };
  });

  const lbW = 200;
  const lbNodes = lbs.map((lb, i) => ({ lb, x: PAD * 2 + i * (lbW + GAP), y: vpcTop + 40, w: lbW, h: 40 }));

  return { boxes, igwNodes, lbNodes, vpcTop, vpcBottom, height };
}

function chipCenter(box: Box, idx: number) {
  return { x: box.x + box.w / 2, y: box.y + SUB_HEADER + idx * (CHIP_H + CHIP_GAP) + CHIP_H / 2 };
}

export function TopologyDiagram({ topology }: { topology: Topology }) {
  const t = topology;
  const { boxes, igwNodes, lbNodes, vpcTop, vpcBottom, height } = layoutTopology(t);

  // Connectors derived from route tables
  const igwLinks: { from: Box; to: (typeof igwNodes)[number] }[] = [];
  const natLinks: { from: Box; toBox: Box; idx: number }[] = [];
  for (const b of boxes) {
    const rt = routeTableFor(b.subnet, t.route_tables);
    for (const r of rt?.routes ?? []) {
      if (r.target_id.startsWith("igw-")) {
        const n = igwNodes.find((g) => g.igw.internet_gateway_id === r.target_id);
        if (n) igwLinks.push({ from: b, to: n });
      } else if (r.target_id.startsWith("nat-")) {
        const toBox = boxes.find((x) => x.chips.some((c) => c.id === r.target_id));
        if (toBox) natLinks.push({ from: b, toBox, idx: toBox.chips.findIndex((c) => c.id === r.target_id) });
      }
    }
  }

  const publicCount = boxes.filter((b) => b.subnet.is_public).length;
  const summary = `Topology of ${t.vpc.vpc_id} (${t.vpc.cidr_block}): ${t.subnets.length} subnets (${publicCount} public), ${t.instances.length} instances, ${igwNodes.length} internet gateways, ${t.nat_gateways.length} NAT gateways, ${t.load_balancers?.length ?? 0} load balancers.`;

  return (
    <div className="grid gap-3">
      <div className="overflow-x-auto rounded-md border bg-background">
        <svg
          viewBox={`0 0 ${W} ${height}`}
          className="h-auto w-full min-w-[640px] text-foreground"
          role="img"
          aria-label={summary}
          data-testid="vpc-topology"
        >
          <defs>
            <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" className="fill-sky-600 dark:fill-sky-400" />
            </marker>
            <marker id="arrow-nat" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" className="fill-violet-600 dark:fill-violet-400" />
            </marker>
          </defs>

          {/* Internet */}
          {igwNodes.length > 0 && (
            <g>
              <rect x={W / 2 - 70} y={8} width={140} height={30} rx={15} className="fill-muted stroke-border" />
              <text x={W / 2} y={28} textAnchor="middle" className="fill-current text-[12px] font-medium">
                Internet
              </text>
              {igwNodes.map((n) => (
                <line key={n.igw.internet_gateway_id} x1={W / 2} y1={38} x2={n.x + n.w / 2} y2={n.y} className="stroke-muted-foreground" strokeWidth={1.2} />
              ))}
            </g>
          )}

          {/* VPC box */}
          <rect x={PAD} y={vpcTop} width={W - PAD * 2} height={vpcBottom - vpcTop} rx={10} className="fill-sky-500/5 stroke-sky-600 dark:stroke-sky-400" strokeWidth={1.5} />
          <text x={PAD + 12} y={vpcTop + 22} className="fill-current text-[13px] font-semibold">
            {trunc(`VPC ${t.vpc.name ? `${t.vpc.name} · ` : ""}${t.vpc.vpc_id}`, 60)}
          </text>
          <text x={PAD + 12} y={vpcTop + 38} className="fill-muted-foreground font-mono text-[11px]">
            {t.vpc.cidr_block}
          </text>

          {/* Load balancers */}
          {lbNodes.map((n) => (
            <g key={n.lb.load_balancer_id ?? n.lb.name}>
              {(n.lb.subnet_ids ?? []).map((sid) => {
                const b = boxes.find((x) => x.subnet.subnet_id === sid);
                if (!b) return null;
                return (
                  <line key={sid} x1={n.x + n.w / 2} y1={n.y + n.h} x2={b.x + b.w / 2} y2={b.y} className="stroke-amber-500" strokeDasharray="2 3" strokeWidth={1.2} />
                );
              })}
              <rect x={n.x} y={n.y} width={n.w} height={n.h} rx={6} className="fill-amber-500/10 stroke-amber-500" />
              <text x={n.x + 8} y={n.y + 16} className="fill-current text-[11px] font-medium">
                {trunc(`LB ${n.lb.name ?? n.lb.load_balancer_id ?? ""}`, 28)}
              </text>
              <text x={n.x + 8} y={n.y + 31} className="fill-muted-foreground text-[10px]">
                {trunc(`${n.lb.scheme ?? ""} ${n.lb.state ?? ""}`, 30)}
              </text>
            </g>
          ))}

          {/* Subnets */}
          {boxes.map((b) => {
            const pub = b.subnet.is_public;
            const rt = routeTableFor(b.subnet, t.route_tables);
            const maxChars = Math.floor(b.w / 7);
            return (
              <g key={b.subnet.subnet_id} data-subnet={b.subnet.subnet_id}>
                <rect
                  x={b.x}
                  y={b.y}
                  width={b.w}
                  height={b.h}
                  rx={8}
                  className={pub ? "fill-emerald-500/10 stroke-emerald-600 dark:stroke-emerald-400" : "fill-blue-500/10 stroke-blue-600 dark:stroke-blue-400"}
                  strokeWidth={1.2}
                />
                <text x={b.x + 10} y={b.y + 18} className="fill-current text-[12px] font-semibold">
                  {trunc(`${b.subnet.name || b.subnet.subnet_id}`, maxChars - 10)}
                </text>
                <text
                  x={b.x + b.w - 10}
                  y={b.y + 18}
                  textAnchor="end"
                  className={pub ? "fill-emerald-700 text-[10px] font-semibold dark:fill-emerald-400" : "fill-blue-700 text-[10px] font-semibold dark:fill-blue-400"}
                >
                  {pub ? "PUBLIC" : "PRIVATE"}
                </text>
                <text x={b.x + 10} y={b.y + 34} className="fill-current font-mono text-[11px]">
                  {b.subnet.cidr_block} · {b.subnet.availability_zone}
                </text>
                <text x={b.x + 10} y={b.y + 49} className="fill-muted-foreground text-[10px]">
                  {trunc(`${b.subnet.subnet_id} · ${rt ? rt.route_table_id : "no route table"}`, maxChars)}
                </text>
                {b.chips.map((c, i) => {
                  const cy = b.y + SUB_HEADER + i * (CHIP_H + CHIP_GAP);
                  return (
                    <g key={c.id}>
                      <rect
                        x={b.x + 8}
                        y={cy}
                        width={b.w - 16}
                        height={CHIP_H}
                        rx={4}
                        className={c.kind === "nat" ? "fill-violet-500/15 stroke-violet-500" : "fill-background stroke-border"}
                      />
                      <circle cx={b.x + 18} cy={cy + CHIP_H / 2} r={4} className={STATE_FILL[c.state] ?? "fill-slate-400"} />
                      <text x={b.x + 28} y={cy + 16} className="fill-current text-[11px]">
                        {trunc(`${c.kind === "nat" ? "NAT " : ""}${c.title}`, Math.floor(maxChars * 0.45))}
                      </text>
                      <text x={b.x + b.w - 14} y={cy + 16} textAnchor="end" className="fill-muted-foreground font-mono text-[10px]">
                        {trunc(c.sub, Math.floor(maxChars * 0.5))}
                      </text>
                    </g>
                  );
                })}
              </g>
            );
          })}

          {/* Route connectors (drawn last so they're visible) */}
          {igwLinks.map((l, i) => {
            const x1 = l.from.x + l.from.w / 2 + (i % 3) * 6;
            const y1 = l.from.y;
            const x2 = l.to.x + l.to.w / 2;
            const y2 = l.to.y + l.to.h;
            const my = (y1 + y2) / 2;
            return (
              <path
                key={`igw-${i}`}
                d={`M ${x1} ${y1} C ${x1} ${my}, ${x2} ${my}, ${x2} ${y2 + 2}`}
                fill="none"
                className="stroke-sky-600 dark:stroke-sky-400"
                strokeWidth={1.5}
                markerEnd="url(#arrow)"
                opacity={0.8}
              />
            );
          })}
          {natLinks.map((l, i) => {
            const target = chipCenter(l.toBox, l.idx);
            const x1 = l.from.x + (l.from.x < l.toBox.x ? l.from.w : 0);
            const y1 = l.from.y + l.from.h / 2;
            const x2 = l.toBox.x + (l.from.x < l.toBox.x ? 8 : l.toBox.w - 8);
            const same = l.from === l.toBox;
            const d = same
              ? `M ${l.from.x + 8} ${y1} C ${l.from.x - 20} ${y1}, ${l.from.x - 20} ${target.y}, ${l.from.x + 8} ${target.y}`
              : `M ${x1} ${y1} C ${(x1 + x2) / 2} ${y1}, ${(x1 + x2) / 2} ${target.y}, ${x2} ${target.y}`;
            return (
              <path
                key={`nat-${i}`}
                d={d}
                fill="none"
                className="stroke-violet-600 dark:stroke-violet-400"
                strokeWidth={1.5}
                strokeDasharray="5 4"
                markerEnd="url(#arrow-nat)"
              />
            );
          })}

          {/* IGW nodes on the VPC edge */}
          {igwNodes.map((n) => (
            <g key={n.igw.internet_gateway_id}>
              <rect x={n.x} y={n.y} width={n.w} height={n.h} rx={6} className="fill-sky-600 dark:fill-sky-500" />
              <text x={n.x + n.w / 2} y={n.y + 15} textAnchor="middle" className="fill-white text-[11px] font-semibold">
                Internet gateway
              </text>
              <text x={n.x + n.w / 2} y={n.y + 29} textAnchor="middle" className="fill-white font-mono text-[10px]">
                {trunc(n.igw.internet_gateway_id, 24)}
              </text>
            </g>
          ))}

          {boxes.length === 0 && (
            <text x={W / 2} y={vpcTop + 90} textAnchor="middle" className="fill-muted-foreground text-[12px]">
              This VPC has no subnets yet.
            </text>
          )}
        </svg>
      </div>

      {/* Legend */}
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-label="Legend">
        <li className="flex items-center gap-1.5">
          <span className="inline-block size-3 rounded-sm border border-emerald-600 bg-emerald-500/20" /> Public subnet (route to IGW)
        </li>
        <li className="flex items-center gap-1.5">
          <span className="inline-block size-3 rounded-sm border border-blue-600 bg-blue-500/20" /> Private subnet
        </li>
        <li className="flex items-center gap-1.5">
          <span className="inline-block h-0.5 w-5 bg-sky-600" /> 0.0.0.0/0 → internet gateway
        </li>
        <li className="flex items-center gap-1.5">
          <span className="inline-block w-5 border-t-2 border-dashed border-violet-600" /> Route → NAT gateway
        </li>
        <li className="flex items-center gap-1.5">
          <span className="inline-block w-5 border-t-2 border-dotted border-amber-500" /> Load balancer → subnet
        </li>
        <li className="flex items-center gap-1.5">
          <span className="inline-block size-2 rounded-full bg-emerald-500" /> running
          <span className="ml-1 inline-block size-2 rounded-full bg-amber-500" /> transitional
          <span className="ml-1 inline-block size-2 rounded-full bg-slate-400" /> stopped
        </li>
      </ul>

      {/* Text alternative */}
      <details className="rounded-md border bg-card p-3 text-sm">
        <summary className="cursor-pointer font-medium">Topology as text</summary>
        <p className="mt-2 text-muted-foreground">{summary}</p>
        <ul className="mt-2 grid gap-2">
          {igwNodes.map((n) => (
            <li key={n.igw.internet_gateway_id}>
              Internet gateway <span className="font-mono">{n.igw.internet_gateway_id}</span> ({n.igw.state})
            </li>
          ))}
          {boxes.map((b) => {
            const rt = routeTableFor(b.subnet, t.route_tables);
            return (
              <li key={b.subnet.subnet_id}>
                <span className="font-medium">{b.subnet.is_public ? "Public" : "Private"} subnet</span>{" "}
                <span className="font-mono">{b.subnet.subnet_id}</span> {b.subnet.name && `(${b.subnet.name})`} {b.subnet.cidr_block}
                {rt && (
                  <span className="text-muted-foreground">
                    {" "}
                    — routes: {rt.routes.map((r) => `${r.destination_cidr} → ${r.target_id}`).join(", ")}
                  </span>
                )}
                {b.chips.length > 0 && (
                  <ul className="mt-1 list-disc pl-5 text-muted-foreground">
                    {b.chips.map((c) => (
                      <li key={c.id}>
                        {c.kind === "nat" ? "NAT gateway" : "Instance"} <span className="font-mono">{c.id}</span> {c.title !== c.id && `(${c.title})`} — {c.state}
                        {c.sub && `, ${c.sub}`}
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            );
          })}
        </ul>
      </details>
    </div>
  );
}
