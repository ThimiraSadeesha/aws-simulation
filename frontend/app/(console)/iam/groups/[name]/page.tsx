"use client";

import { useParams } from "next/navigation";
import { GroupDetail } from "@/features/iam/components/groups";

export default function Page() {
  const params = useParams<{ name: string }>();
  return <GroupDetail name={decodeURIComponent(params.name)} />;
}
