"use client";

import { useParams } from "next/navigation";
import { GroupDetail } from "@/features/autoscaling/group-detail";

export default function Page() {
  const { name } = useParams<{ name: string }>();
  return <GroupDetail name={decodeURIComponent(name)} />;
}
