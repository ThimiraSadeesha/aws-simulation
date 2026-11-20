"use client";

import { useParams } from "next/navigation";
import { QueueDetail } from "@/features/sqs/components";

export default function Page() {
  const params = useParams<{ name: string }>();
  return <QueueDetail name={decodeURIComponent(params.name)} />;
}
