"use client";

import { useParams } from "next/navigation";
import { LoadBalancerDetail } from "@/features/elb/load-balancer-detail";

export default function Page() {
  const { id } = useParams<{ id: string }>();
  return <LoadBalancerDetail id={decodeURIComponent(id)} />;
}
