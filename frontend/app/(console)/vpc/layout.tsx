"use client";

import { ServiceNav } from "@/components/shell/service-nav";

const ITEMS = [
  { label: "VPCs", href: "/vpc", exact: true },
  { label: "Subnets", href: "/vpc/subnets" },
  { label: "Route tables", href: "/vpc/route-tables" },
  { label: "Internet gateways", href: "/vpc/internet-gateways" },
  { label: "NAT gateways", href: "/vpc/nat-gateways" },
  { label: "Security groups", href: "/vpc/security-groups" },
  { label: "Network ACLs", href: "/vpc/network-acls" },
  { label: "Network interfaces", href: "/vpc/network-interfaces" },
  { label: "DNS resolver", href: "/vpc/dns" },
  { label: "Reachability Analyzer", href: "/vpc/reachability" },
];

export default function VpcLayout({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <ServiceNav label="VPC" items={ITEMS} />
      {children}
    </div>
  );
}
