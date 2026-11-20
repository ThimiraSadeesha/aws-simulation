"use client";

import { useParams } from "next/navigation";
import { PolicyDetail } from "@/features/iam/components/policies";

export default function Page() {
  const params = useParams<{ name: string }>();
  return <PolicyDetail name={decodeURIComponent(params.name)} />;
}
