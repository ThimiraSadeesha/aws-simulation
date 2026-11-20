"use client";

import { useParams } from "next/navigation";
import { UserDetail } from "@/features/iam/components/user-detail";

export default function Page() {
  const params = useParams<{ name: string }>();
  return <UserDetail name={decodeURIComponent(params.name)} />;
}
