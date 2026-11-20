"use client";

import { ServiceNav } from "@/components/shell/service-nav";

export default function ElbLayout({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <ServiceNav
        label="Load Balancers"
        items={[
          { label: "Load balancers", href: "/elb", exact: true },
          { label: "Target groups", href: "/elb/target-groups" },
        ]}
      />
      {children}
    </div>
  );
}
