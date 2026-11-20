"use client";

import { useParams } from "next/navigation";
import { TargetGroupDetail } from "@/features/elb/target-groups";

export default function Page() {
  const { id } = useParams<{ id: string }>();
  return <TargetGroupDetail id={decodeURIComponent(id)} />;
}
