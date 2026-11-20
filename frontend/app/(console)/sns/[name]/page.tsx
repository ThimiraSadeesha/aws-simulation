"use client";

import { useParams } from "next/navigation";
import { TopicDetail } from "@/features/sns/components";

export default function Page() {
  const params = useParams<{ name: string }>();
  return <TopicDetail name={decodeURIComponent(params.name)} />;
}
