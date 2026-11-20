"use client";

import { useParams } from "next/navigation";
import { RoleDetail } from "@/features/iam/components/roles";

export default function Page() {
  const params = useParams<{ name: string }>();
  return <RoleDetail name={decodeURIComponent(params.name)} />;
}
