"use client";

import { useParams } from "next/navigation";
import { useState } from "react";
import { Play, RotateCw, Square, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { PageHeader } from "@/components/console/page-header";
import { StatusBadge, TRANSITIONAL_STATES } from "@/components/console/status-badge";
import { QueryBoundary } from "@/components/console/states";
import { useInstance, type InstanceAction } from "@/features/ec2/api";
import { canDo, InstanceActionDialog } from "@/features/ec2/instance-actions";
import {
  ConsoleTab,
  DetailsTab,
  HistoryTab,
  MetadataTab,
  MonitoringTab,
  NetworkingTab,
  SecurityTab,
  TagsTab,
} from "@/features/ec2/instance-tabs";

const ACTIONS: { action: InstanceAction; label: string; icon: React.ReactNode; destructive?: boolean }[] = [
  { action: "start", label: "Start", icon: <Play /> },
  { action: "stop", label: "Stop", icon: <Square /> },
  { action: "reboot", label: "Reboot", icon: <RotateCw /> },
  { action: "terminate", label: "Terminate", icon: <Trash2 />, destructive: true },
];

export default function InstanceDetailPage() {
  const { id } = useParams<{ id: string }>();
  const q = useInstance(id);
  const [action, setAction] = useState<InstanceAction | null>(null);
  const [tab, setTab] = useState("details");

  return (
    <QueryBoundary query={q}>
      {(i) => (
        <div>
          <PageHeader
            breadcrumbs={[{ label: "EC2", href: "/ec2" }, { label: "Instances", href: "/ec2" }, { label: i.instance_id }]}
            title={
              <span className="flex flex-wrap items-center gap-2">
                {i.name || i.tags?.Name || i.instance_id}
                <StatusBadge status={i.state} />
                {TRANSITIONAL_STATES.has(i.state) && <span className="text-xs font-normal text-muted-foreground">(refreshing…)</span>}
              </span>
            }
            description={<span className="font-mono text-xs">{i.instance_id}</span>}
            actions={ACTIONS.map((a) => (
              <Button
                key={a.action}
                size="sm"
                variant={a.destructive ? "destructive" : "outline"}
                disabled={!canDo(a.action, i.state)}
                onClick={() => setAction(a.action)}
              >
                {a.icon} {a.label}
              </Button>
            ))}
          />
          {i.state_reason && (
            <p className="mb-3 rounded-md border bg-muted/40 px-3 py-2 text-xs">
              <span className="font-medium">State reason:</span> {i.state_reason}
            </p>
          )}
          <Tabs value={tab} onValueChange={setTab}>
            <TabsList className="flex-wrap">
              <TabsTrigger value="details">Details</TabsTrigger>
              <TabsTrigger value="monitoring">Monitoring</TabsTrigger>
              <TabsTrigger value="security">Security</TabsTrigger>
              <TabsTrigger value="networking">Networking</TabsTrigger>
              <TabsTrigger value="tags">Tags</TabsTrigger>
              <TabsTrigger value="console">Console output</TabsTrigger>
              <TabsTrigger value="metadata">Metadata</TabsTrigger>
              <TabsTrigger value="history">State history</TabsTrigger>
            </TabsList>
            <div className="mt-3">
              <TabsContent value="details">
                <DetailsTab instance={i} />
              </TabsContent>
              <TabsContent value="monitoring">{tab === "monitoring" && <MonitoringTab instance={i} />}</TabsContent>
              <TabsContent value="security">{tab === "security" && <SecurityTab instance={i} />}</TabsContent>
              <TabsContent value="networking">
                <NetworkingTab instance={i} />
              </TabsContent>
              <TabsContent value="tags">{tab === "tags" && <TagsTab key={JSON.stringify(i.tags)} instance={i} />}</TabsContent>
              <TabsContent value="console">{tab === "console" && <ConsoleTab instanceId={i.instance_id} />}</TabsContent>
              <TabsContent value="metadata">{tab === "metadata" && <MetadataTab instanceId={i.instance_id} />}</TabsContent>
              <TabsContent value="history">{tab === "history" && <HistoryTab instanceId={i.instance_id} />}</TabsContent>
            </div>
          </Tabs>
          <InstanceActionDialog action={action} ids={[i.instance_id]} onClose={() => setAction(null)} />
        </div>
      )}
    </QueryBoundary>
  );
}
