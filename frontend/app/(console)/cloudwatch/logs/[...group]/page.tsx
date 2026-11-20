"use client";

import { useParams } from "next/navigation";
import { LogGroupDetail } from "@/features/cloudwatch/logs";

/**
 * Log group names contain slashes (e.g. /cloudsim/ec2/console). Links encode the whole name as one
 * segment; a catch-all route also accepts unencoded paths. Leading "/" is restored when segments were split.
 */
export default function Page() {
  const { group } = useParams<{ group: string[] }>();
  const parts = (Array.isArray(group) ? group : [group]).map((g) => decodeURIComponent(g));
  const name = parts.length === 1 ? parts[0] : `/${parts.join("/")}`.replace(/^\/\//, "/");
  return <LogGroupDetail name={name} />;
}
