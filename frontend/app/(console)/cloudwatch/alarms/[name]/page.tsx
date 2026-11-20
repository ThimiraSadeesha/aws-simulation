"use client";

import { useParams } from "next/navigation";
import { AlarmDetail } from "@/features/cloudwatch/alarms";

export default function Page() {
  const { name } = useParams<{ name: string }>();
  return <AlarmDetail name={decodeURIComponent(name)} />;
}
